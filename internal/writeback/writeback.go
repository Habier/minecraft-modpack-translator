package writeback

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	OutputDirectory  = "modpack-translator-output"
	ResourcePackName = "ModpackTranslations"
	SourceLang       = "en_us"
	TargetLang       = "es_es"
	ZipName          = "modpack-translations-es_es.zip"
)

var zipModTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

func Workspace(modpackPath string) (string, error) {
	paths := newWritebackPaths(modpackPath)
	if err := RemoveStaleZip(modpackPath); err != nil {
		return "", err
	}

	plan, err := loadWritebackPlan(paths)
	if err != nil {
		return "", err
	}
	if err := writeStandardLang(paths.exportPack, plan.standardLang); err != nil {
		return "", err
	}
	if err := writeKubeJSLang(paths.workspace, paths.exportOverrides, plan.kubeJSLang); err != nil {
		return "", err
	}
	if err := writeFTBQuests(paths.workspace, paths.exportOverrides, plan.ftbQuestSNBT); err != nil {
		return "", err
	}
	if len(plan.patchouli) > 0 {
		if err := writePatchouli(paths.workspace, paths.exportPack, plan.patchouli, plan.translated); err != nil {
			return "", fmt.Errorf("writing Patchouli files: %w", err)
		}
	}

	if err := createOverridesZip(paths.exportOverrides, paths.zipPath); err != nil {
		return "", fmt.Errorf("creating ZIP: %w", err)
	}
	fmt.Printf("\nShareable ZIP: %s\n", paths.zipPath)
	fmt.Println("\nDone. To use in Minecraft:")
	fmt.Println("  1. Share or extract the ZIP into the Minecraft instance root:")
	fmt.Println("     " + paths.zipPath)
	fmt.Println("  2. Enable 'ModpackTranslations' in-game (Options > Resource Packs)")
	return paths.zipPath, nil
}

func ZipPath(modpackPath string) string {
	return filepath.Join(modpackPath, OutputDirectory, "export", ZipName)
}

func RemoveStaleZip(modpackPath string) error {
	if err := os.Remove(ZipPath(modpackPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale ZIP: %w", err)
	}
	return nil
}

type writebackPaths struct {
	workspace       string
	exportPack      string
	exportOverrides string
	zipPath         string
}

func newWritebackPaths(modpackPath string) writebackPaths {
	root := filepath.Join(modpackPath, OutputDirectory)
	return writebackPaths{
		workspace:       filepath.Join(root, "workspace"),
		exportPack:      filepath.Join(root, "export", "overrides", "resourcepacks", ResourcePackName),
		exportOverrides: filepath.Join(root, "export", "overrides"),
		zipPath:         ZipPath(modpackPath),
	}
}

type writebackPlan struct {
	translated   map[string]string
	standardLang map[string]map[string]string
	kubeJSLang   map[string]map[string]string
	patchouli    []patchouliWriteback
	ftbQuestSNBT map[string]ftbEntry
}

func loadWritebackPlan(paths writebackPaths) (writebackPlan, error) {
	catalogPath := filepath.Join(paths.workspace, "catalog", "catalog.v1.json")
	cachePath := filepath.Join(paths.workspace, "translations", "translations.v2.json")

	catalog, err := loadCatalog(catalogPath)
	if err != nil {
		return writebackPlan{}, fmt.Errorf("loading catalog: %w", err)
	}
	fmt.Printf("Catalog: %d entries\n", len(catalog.Entries))

	cache, err := loadTranslationCache(cachePath)
	if err != nil {
		return writebackPlan{}, fmt.Errorf("loading translations: %w", err)
	}
	fmt.Printf("Translations: %d entries\n", len(cache.Entries))

	translated := make(map[string]string, len(cache.Entries))
	for _, entry := range cache.Entries {
		translated[entry.ID] = entry.Translation
	}

	plan := writebackPlan{
		translated:   translated,
		standardLang: make(map[string]map[string]string),
		kubeJSLang:   make(map[string]map[string]string),
		ftbQuestSNBT: make(map[string]ftbEntry),
	}
	var skipped, matched int

	for _, entry := range catalog.Entries {
		translation, ok := translated[entry.ID]
		if !ok {
			skipped++
			continue
		}
		matched++

		switch entry.SourceKind {
		case "standard_lang":
			ns := extractNamespace(entry.SourceFile)
			if ns == "" {
				fmt.Fprintf(os.Stderr, "Warning: cannot extract namespace from %q\n", entry.SourceFile)
				continue
			}
			key := pointerKey(entry.Locator)
			if plan.standardLang[ns] == nil {
				plan.standardLang[ns] = make(map[string]string)
			}
			plan.standardLang[ns][key] = translation

		case "kubejs_lang":
			key := pointerKey(entry.Locator)
			if plan.kubeJSLang[entry.SourceFile] == nil {
				plan.kubeJSLang[entry.SourceFile] = make(map[string]string)
			}
			plan.kubeJSLang[entry.SourceFile][key] = translation

		case "patchouli":
			plan.patchouli = append(plan.patchouli, patchouliWriteback{
				id:         entry.ID,
				sourceFile: entry.SourceFile,
				locator:    entry.Locator,
				source:     entry.Source,
				writeback:  entry.Writeback,
			})

		case "ftbquests_snbt", "ftbquests_json5":
			sf := entry.SourceFile
			if _, exists := plan.ftbQuestSNBT[sf]; !exists {
				plan.ftbQuestSNBT[sf] = ftbEntry{
					sourceFile: sf,
					format:     entry.Writeback.Format,
					fields:     make(map[string]fieldInfo),
				}
			}
			fe := plan.ftbQuestSNBT[sf]
			fe.fields[entry.Locator] = fieldInfo{
				source:      entry.Source,
				translation: translation,
				valueType:   entry.Writeback.ValueType,
				container:   entry.Writeback.Container,
				arrayIndex:  entry.Writeback.ArrayIndex,
				sourceHash:  entry.Writeback.SourceSHA256,
			}
			plan.ftbQuestSNBT[sf] = fe
		}
	}

	fmt.Printf("  Matched: %d, Skipped (no translation): %d\n", matched, skipped)
	fmt.Printf("  Standard lang namespaces: need to resolve\n")
	fmt.Printf("  KubeJS lang files: %d\n", len(plan.kubeJSLang))
	fmt.Printf("  Patchouli entries: %d\n", len(plan.patchouli))
	fmt.Printf("  FTB Quests files: %d\n", len(plan.ftbQuestSNBT))
	if skipped > 0 {
		return writebackPlan{}, fmt.Errorf("translation coverage is incomplete: %d of %d catalog entries are missing translations; ZIP was not created", skipped, len(catalog.Entries))
	}

	return plan, nil
}

func writeStandardLang(exportPack string, nsGroups map[string]map[string]string) error {
	nsList := make([]string, 0, len(nsGroups))
	for ns := range nsGroups {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)

	for _, ns := range nsList {
		dir := filepath.Join(exportPack, "assets", ns, "lang")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating standard language directory %s: %w", dir, err)
		}
		path := filepath.Join(dir, TargetLang+".json")
		values := nsGroups[ns]
		data, err := json.MarshalIndent(values, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling standard language file %s: %w", path, err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("writing standard language file %s: %w", path, err)
		}
		fmt.Printf("  [LANG] %s: %d keys -> %s\n", ns, len(values), path)
	}
	return nil
}

func writeKubeJSLang(workspace, exportOverrides string, kubeJSLang map[string]map[string]string) error {
	if len(kubeJSLang) == 0 {
		return nil
	}
	sourceFiles := make([]string, 0, len(kubeJSLang))
	for sourceFile := range kubeJSLang {
		sourceFiles = append(sourceFiles, sourceFile)
	}
	sort.Strings(sourceFiles)
	for _, sourceFile := range sourceFiles {
		values := kubeJSLang[sourceFile]
		srcPath := filepath.Join(workspace, filepath.FromSlash(sourceFile))
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading KubeJS language source %s: %w", srcPath, err)
		}
		keys, err := orderedJSONKeys(data)
		if err != nil {
			return fmt.Errorf("reading KubeJS language key order from %s: %w", srcPath, err)
		}
		ns := kubeJSNamespace(sourceFile)
		if ns == "" {
			return fmt.Errorf("cannot extract KubeJS namespace from %q", sourceFile)
		}
		path := filepath.Join(exportOverrides, "kubejs", "assets", ns, "lang", TargetLang+".json")
		out, err := marshalOrderedStringObject(keys, values)
		if err != nil {
			return fmt.Errorf("marshaling KubeJS language file %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("creating KubeJS language directory %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, out, 0644); err != nil {
			return fmt.Errorf("writing KubeJS language file %s: %w", path, err)
		}
		fmt.Printf("  [KUBEJS] %s: %d keys -> %s\n", ns, len(values), path)
	}
	return nil
}

func writeFTBQuests(workspace, exportOverrides string, ftbQuestSNBT map[string]ftbEntry) error {
	if len(ftbQuestSNBT) == 0 {
		return nil
	}
	sfList := make([]string, 0, len(ftbQuestSNBT))
	for sf := range ftbQuestSNBT {
		sfList = append(sfList, sf)
	}
	sort.Strings(sfList)

	for _, sf := range sfList {
		fe := ftbQuestSNBT[sf]
		rel := strings.TrimPrefix(sf, "sources/ftbquests/")
		rel = strings.TrimPrefix(rel, "/")
		srcPath := filepath.Join(workspace, filepath.FromSlash(sf))
		exportPath := filepath.Join(exportOverrides, filepath.FromSlash(rel))

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading FTB Quests source %s: %w", srcPath, err)
		}

		content, err := applyFTBWriteback(data, fe)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
			return fmt.Errorf("creating FTB Quests directory for %s: %w", exportPath, err)
		}
		if err := os.WriteFile(exportPath, content, 0644); err != nil {
			return fmt.Errorf("writing FTB Quests file %s: %w", exportPath, err)
		}
		fmt.Printf("  [FTB] %s -> %s\n", filepath.Base(sf), exportPath)
	}
	return nil
}

func createOverridesZip(overridesDir, zipPath string) error {
	files := []string{}
	if err := filepath.WalkDir(overridesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk overrides: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j]) })

	if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
		return fmt.Errorf("create ZIP directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(zipPath), "."+filepath.Base(zipPath)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create ZIP staging file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	zw := zip.NewWriter(tmp)
	for _, path := range files {
		rel, err := filepath.Rel(overridesDir, path)
		if err != nil {
			zw.Close()
			tmp.Close()
			return fmt.Errorf("resolve ZIP path for %s: %w", path, err)
		}
		header := &zip.FileHeader{
			Name:   filepath.ToSlash(rel),
			Method: zip.Deflate,
		}
		header.SetMode(0644)
		header.SetModTime(zipModTime)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			zw.Close()
			tmp.Close()
			return fmt.Errorf("create ZIP entry %s: %w", header.Name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			zw.Close()
			tmp.Close()
			return fmt.Errorf("read ZIP source %s: %w", path, err)
		}
		if _, err := writer.Write(data); err != nil {
			zw.Close()
			tmp.Close()
			return fmt.Errorf("write ZIP entry %s: %w", header.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return fmt.Errorf("close ZIP writer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close ZIP staging file: %w", err)
	}
	if err := os.Rename(tmpName, zipPath); err != nil {
		return fmt.Errorf("publish ZIP: %w", err)
	}
	return nil
}

type catalogV1 struct {
	Schema       string           `json:"schema"`
	SourceLocale string           `json:"source_locale"`
	TargetLocale string           `json:"target_locale"`
	Entries      []catalogEntryV1 `json:"entries"`
}

type catalogEntryV1 struct {
	ID           string             `json:"id"`
	SourceKind   string             `json:"source_kind"`
	SourceFile   string             `json:"source_file"`
	Locator      string             `json:"locator"`
	Source       string             `json:"source"`
	TargetLocale string             `json:"target_locale"`
	Tokens       []catalogTokenV1   `json:"tokens"`
	Writeback    catalogWritebackV1 `json:"writeback"`
}

type catalogTokenV1 struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type catalogWritebackV1 struct {
	Format       string `json:"format"`
	ValueType    string `json:"value_type"`
	Container    string `json:"container"`
	ArrayIndex   *int   `json:"array_index,omitempty"`
	SourceSHA256 string `json:"source_file_sha256"`
	Encoding     string `json:"encoding"`
}

type translationCacheV2 struct {
	Schema        string               `json:"schema"`
	TargetLocale  string               `json:"target_locale"`
	PromptVersion string               `json:"prompt_version"`
	Entries       []translationEntryV2 `json:"entries"`
}

type translationEntryV2 struct {
	ID                string `json:"id"`
	CacheKey          string `json:"cache_key"`
	SourceSHA256      string `json:"source_sha256"`
	TokenSignature    string `json:"token_signature"`
	Translation       string `json:"translation"`
	TranslationSHA256 string `json:"translation_sha256"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
}

type patchouliWriteback struct {
	id         string
	sourceFile string
	locator    string
	source     string
	writeback  catalogWritebackV1
}

type ftbEntry struct {
	sourceFile string
	format     string
	fields     map[string]fieldInfo
}

type fieldInfo struct {
	source      string
	translation string
	valueType   string
	container   string
	arrayIndex  *int
	sourceHash  string
}

func writePatchouli(workspace, exportPack string, entries []patchouliWriteback, translated map[string]string) error {
	type filePatch struct {
		sourceFile string
		fields     map[string]string
	}
	byFile := make(map[string]*filePatch)

	for _, e := range entries {
		t, ok := translated[e.id]
		if !ok {
			continue
		}
		if byFile[e.sourceFile] == nil {
			byFile[e.sourceFile] = &filePatch{sourceFile: e.sourceFile, fields: make(map[string]string)}
		}
		byFile[e.sourceFile].fields[e.locator] = t
	}

	for _, fp := range byFile {
		srcPath := filepath.Join(workspace, filepath.FromSlash(fp.sourceFile))
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", srcPath, err)
		}

		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", srcPath, err)
		}

		for locator, translation := range fp.fields {
			parts := strings.Split(strings.TrimPrefix(locator, "/"), "/")
			if err := setNested(root, parts, translation); err != nil {
				return fmt.Errorf("set %s in %s: %w", locator, fp.sourceFile, err)
			}
		}

		exportPath, err := patchouliExportPath(fp.sourceFile, exportPack)
		if err != nil {
			return fmt.Errorf("resolve export path for %s: %w", fp.sourceFile, err)
		}
		if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
			return fmt.Errorf("create directory for %s: %w", exportPath, err)
		}

		out, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal %s: %w", exportPath, err)
		}
		out = append(out, '\n')
		if err := os.WriteFile(exportPath, out, 0644); err != nil {
			return fmt.Errorf("write %s: %w", exportPath, err)
		}
		fmt.Printf("  [PATCHOULI] %s -> %s\n", filepath.Base(fp.sourceFile), exportPath)
	}

	return nil
}

func patchouliExportPath(sourceFile, exportPack string) (string, error) {
	sf := filepath.ToSlash(sourceFile)
	parts := strings.Split(sf, "/")

	for i, part := range parts {
		if part == "patchouli_books" && i+2 < len(parts) {
			ns := parts[i-1]
			book := parts[i+1]
			remaining := parts[i+2:]
			for j, p := range remaining {
				if p == SourceLang || strings.HasPrefix(p, SourceLang+"/") {
					remaining[j] = TargetLang + strings.TrimPrefix(p, SourceLang)
					break
				}
			}
			relParts := append([]string{"assets", ns, "patchouli_books", book}, remaining...)
			rel := filepath.Join(relParts...)
			return filepath.Join(exportPack, rel), nil
		}
	}

	if len(parts) >= 4 && parts[0] == "sources" && parts[1] == "patchouli" {
		relParts := parts[3:]
		for i, p := range relParts {
			if p == SourceLang || strings.HasPrefix(p, SourceLang+"/") {
				relParts[i] = TargetLang + strings.TrimPrefix(p, SourceLang)
			}
		}
		rel := filepath.Join(relParts...)
		return filepath.Join(exportPack, rel), nil
	}

	return "", fmt.Errorf("unrecognized patchouli source path structure: %s", sourceFile)
}

func setNested(root map[string]any, parts []string, value string) error {
	type node struct {
		obj map[string]any
		arr []any
		idx int
	}

	current := node{obj: root}
	pos := 0

	for pos < len(parts) {
		part := parts[pos]
		isLast := pos == len(parts)-1

		if idx, err := parseInt(part); err == nil {
			if current.arr == nil {
				return fmt.Errorf("cannot index %q into a non-array", part)
			}
			if idx < 0 || idx >= len(current.arr) {
				return fmt.Errorf("index %d out of range (len=%d)", idx, len(current.arr))
			}
			if isLast {
				if _, ok := current.arr[idx].(string); ok {
					current.arr[idx] = value
					return nil
				}
			}
			elem, ok := current.arr[idx].(map[string]any)
			if !ok {
				if isLast {
					current.arr[idx] = value
					return nil
				}
				return fmt.Errorf("array element %d is not an object (got %T)", idx, current.arr[idx])
			}
			current = node{obj: elem}
			pos++
			continue
		}

		if current.obj == nil {
			return fmt.Errorf("cannot read field %q from a non-object", part)
		}
		next, exists := current.obj[part]
		if !exists {
			return fmt.Errorf("key %q not found", part)
		}
		if isLast {
			current.obj[part] = value
			return nil
		}

		nextPart := parts[pos+1]
		if _, err := parseInt(nextPart); err == nil {
			arr, ok := next.([]any)
			if !ok {
				return fmt.Errorf("expected array at %s, got %T", part, next)
			}
			current = node{arr: arr}
			pos++
			continue
		}

		nextObj, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object at %s, got %T", part, next)
		}
		current = node{obj: nextObj}
		pos++
	}

	return nil
}

type ftbReplacement struct {
	start int
	end   int
	value string
}

func applyFTBWriteback(data []byte, entry ftbEntry) ([]byte, error) {
	digest := sha256.Sum256(data)
	actualHash := hex.EncodeToString(digest[:])
	locators := make([]string, 0, len(entry.fields))
	for locator := range entry.fields {
		locators = append(locators, locator)
	}
	sort.Strings(locators)

	replacements := make([]ftbReplacement, 0, len(locators))
	seenSpans := map[[2]int]string{}
	for _, locator := range locators {
		info := entry.fields[locator]
		if info.sourceHash == "" || info.sourceHash != actualHash {
			return nil, fmt.Errorf("FTB Quests source hash mismatch for %s%s", entry.sourceFile, locator)
		}
		if info.valueType != "string" {
			return nil, fmt.Errorf("FTB Quests %s%s has unsupported value type %q", entry.sourceFile, locator, info.valueType)
		}
		parts := ftbLocatorParts(locator)
		if len(parts) == 0 {
			return nil, fmt.Errorf("FTB Quests %s has empty locator", entry.sourceFile)
		}
		if info.container == "array" {
			if info.arrayIndex == nil || parts[len(parts)-1] != fmt.Sprint(*info.arrayIndex) {
				return nil, fmt.Errorf("FTB Quests %s%s array index metadata mismatch", entry.sourceFile, locator)
			}
		} else if info.container == "object" {
			if info.arrayIndex != nil {
				return nil, fmt.Errorf("FTB Quests %s%s object metadata cannot include array index", entry.sourceFile, locator)
			}
		} else {
			return nil, fmt.Errorf("FTB Quests %s%s has unsupported container %q", entry.sourceFile, locator, info.container)
		}
		start, end, source, err := findFTBString(data, parts, entry.format == "json5")
		if err != nil {
			return nil, fmt.Errorf("locate FTB Quests %s%s: %w", entry.sourceFile, locator, err)
		}
		if source != info.source {
			return nil, fmt.Errorf("FTB Quests source mismatch for %s%s", entry.sourceFile, locator)
		}
		span := [2]int{start, end}
		if previous, exists := seenSpans[span]; exists {
			return nil, fmt.Errorf("FTB Quests locators %s and %s target the same source span", previous, locator)
		}
		seenSpans[span] = locator
		replacements = append(replacements, ftbReplacement{start: start, end: end, value: info.translation})
	}

	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	out := append([]byte(nil), data...)
	for _, replacement := range replacements {
		encoded, err := json.Marshal(replacement.value)
		if err != nil {
			return nil, err
		}
		out = append(out[:replacement.start], append(encoded, out[replacement.end:]...)...)
	}
	return out, nil
}

func ftbLocatorParts(locator string) []string {
	trimmed := strings.TrimPrefix(locator, "/")
	if trimmed == "" {
		return nil
	}
	raw := strings.Split(trimmed, "/")
	parts := make([]string, len(raw))
	for i, part := range raw {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		parts[i] = part
	}
	return parts
}

func findFTBString(data []byte, parts []string, allowComments bool) (int, int, string, error) {
	pos, err := skipFTBSpace(data, 0, allowComments)
	if err != nil {
		return 0, 0, "", err
	}
	start, end, value, err := findFTBStringInValue(data, pos, parts, allowComments)
	if err != nil {
		return 0, 0, "", err
	}
	return start, end, value, nil
}

func findFTBStringInValue(data []byte, pos int, parts []string, allowComments bool) (int, int, string, error) {
	var err error
	pos, err = skipFTBSpace(data, pos, allowComments)
	if err != nil {
		return 0, 0, "", err
	}
	if pos >= len(data) {
		return 0, 0, "", fmt.Errorf("unexpected end of file")
	}
	if data[pos] == '{' {
		return findFTBStringInObject(data, pos, parts, allowComments)
	}
	if data[pos] == '[' {
		return findFTBStringInArray(data, pos, parts, allowComments)
	}
	return 0, 0, "", fmt.Errorf("cannot descend into %q", data[pos])
}

func findFTBStringInObject(data []byte, pos int, parts []string, allowComments bool) (int, int, string, error) {
	pos++
	for {
		var err error
		pos, err = skipFTBSpaceAndComma(data, pos, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		if pos >= len(data) {
			return 0, 0, "", fmt.Errorf("unterminated object")
		}
		if data[pos] == '}' {
			return 0, 0, "", fmt.Errorf("key %q not found", parts[0])
		}
		key, next, err := parseFTBKey(data, pos, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		pos, err = skipFTBSpace(data, next, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		if pos >= len(data) || data[pos] != ':' {
			return 0, 0, "", fmt.Errorf("missing colon after key %q", key)
		}
		valuePos, err := skipFTBSpace(data, pos+1, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		if key == parts[0] {
			if len(parts) == 1 {
				return parseFTBStringAt(data, valuePos)
			}
			return findFTBStringInValue(data, valuePos, parts[1:], allowComments)
		}
		pos, err = skipFTBValue(data, valuePos, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
	}
}

func findFTBStringInArray(data []byte, pos int, parts []string, allowComments bool) (int, int, string, error) {
	target, err := parseInt(parts[0])
	if err != nil {
		return 0, 0, "", fmt.Errorf("array locator %q is not numeric", parts[0])
	}
	pos++
	index := 0
	for {
		var err error
		pos, err = skipFTBSpaceAndComma(data, pos, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		if pos >= len(data) {
			return 0, 0, "", fmt.Errorf("unterminated array")
		}
		if data[pos] == ']' {
			return 0, 0, "", fmt.Errorf("array index %d out of range", target)
		}
		if index == target {
			if len(parts) == 1 {
				return parseFTBStringAt(data, pos)
			}
			return findFTBStringInValue(data, pos, parts[1:], allowComments)
		}
		pos, err = skipFTBValue(data, pos, allowComments)
		if err != nil {
			return 0, 0, "", err
		}
		index++
	}
}

func parseFTBKey(data []byte, pos int, allowComments bool) (string, int, error) {
	if pos < len(data) && (data[pos] == '"' || data[pos] == '\'') {
		_, end, value, err := parseFTBStringAt(data, pos)
		return value, end, err
	}
	start := pos
	for pos < len(data) && data[pos] != ':' && !isFTBSpace(data[pos]) {
		if allowComments {
			next, err := skipFTBComment(data, pos)
			if err != nil {
				return "", 0, err
			}
			if next != pos {
				break
			}
		}
		pos++
	}
	if start == pos {
		return "", 0, fmt.Errorf("missing object key")
	}
	return string(data[start:pos]), pos, nil
}

func parseFTBStringAt(data []byte, pos int) (int, int, string, error) {
	if pos >= len(data) || (data[pos] != '"' && data[pos] != '\'') {
		return 0, 0, "", fmt.Errorf("value is not a quoted string")
	}
	quote := data[pos]
	var builder strings.Builder
	for i := pos + 1; i < len(data); i++ {
		c := data[i]
		if c == quote {
			return pos, i + 1, builder.String(), nil
		}
		if c == '\\' {
			if i+1 >= len(data) {
				return 0, 0, "", fmt.Errorf("unterminated escape")
			}
			i++
			switch escaped := data[i]; escaped {
			case '"', '\'', '\\', '/':
				builder.WriteByte(escaped)
			case 'b':
				builder.WriteByte('\b')
			case 'f':
				builder.WriteByte('\f')
			case 'n':
				builder.WriteByte('\n')
			case 'r':
				builder.WriteByte('\r')
			case 't':
				builder.WriteByte('\t')
			case 'u':
				if i+4 >= len(data) {
					return 0, 0, "", fmt.Errorf("unterminated unicode escape")
				}
				value, err := strconv.ParseInt(string(data[i+1:i+5]), 16, 32)
				if err != nil {
					return 0, 0, "", fmt.Errorf("invalid unicode escape")
				}
				builder.WriteRune(rune(value))
				i += 4
			default:
				builder.WriteByte(escaped)
			}
			continue
		}
		builder.WriteByte(c)
	}
	return 0, 0, "", fmt.Errorf("unterminated string")
}

func skipFTBValue(data []byte, pos int, allowComments bool) (int, error) {
	pos, err := skipFTBSpace(data, pos, allowComments)
	if err != nil {
		return 0, err
	}
	if pos >= len(data) {
		return 0, fmt.Errorf("unexpected end of file")
	}
	switch data[pos] {
	case '"', '\'':
		_, end, _, err := parseFTBStringAt(data, pos)
		return end, err
	case '{':
		return skipFTBDelimited(data, pos, '{', '}', allowComments)
	case '[':
		return skipFTBDelimited(data, pos, '[', ']', allowComments)
	default:
		for pos < len(data) && data[pos] != ',' && data[pos] != ']' && data[pos] != '}' && !isFTBSpace(data[pos]) {
			if next, err := skipFTBComment(data, pos); err != nil {
				return 0, err
			} else if next != pos {
				if !allowComments {
					return 0, fmt.Errorf("comments are not supported in FTB quest SNBT")
				}
				break
			}
			pos++
		}
		return pos, nil
	}
}

func skipFTBDelimited(data []byte, pos int, open, close byte, allowComments bool) (int, error) {
	depth := 0
	for pos < len(data) {
		switch data[pos] {
		case '"', '\'':
			_, end, _, err := parseFTBStringAt(data, pos)
			if err != nil {
				return 0, err
			}
			pos = end
			continue
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return pos + 1, nil
			}
		case '/':
			next, err := skipFTBComment(data, pos)
			if err != nil {
				return 0, err
			}
			if next != pos {
				if !allowComments {
					return 0, fmt.Errorf("comments are not supported in FTB quest SNBT")
				}
				pos = next
				continue
			}
		}
		pos++
	}
	return 0, fmt.Errorf("unterminated %c", open)
}

func skipFTBSpaceAndComma(data []byte, pos int, allowComments bool) (int, error) {
	pos, err := skipFTBSpace(data, pos, allowComments)
	if err != nil {
		return 0, err
	}
	if pos < len(data) && data[pos] == ',' {
		pos, err = skipFTBSpace(data, pos+1, allowComments)
		if err != nil {
			return 0, err
		}
	}
	return pos, nil
}

func skipFTBSpace(data []byte, pos int, allowComments bool) (int, error) {
	for pos < len(data) {
		if isFTBSpace(data[pos]) {
			pos++
			continue
		}
		if !allowComments {
			break
		}
		next, err := skipFTBComment(data, pos)
		if err != nil {
			return 0, err
		}
		if next == pos {
			break
		}
		pos = next
	}
	return pos, nil
}

func skipFTBComment(data []byte, pos int) (int, error) {
	if pos+1 >= len(data) || data[pos] != '/' {
		return pos, nil
	}
	switch data[pos+1] {
	case '/':
		pos += 2
		for pos < len(data) && data[pos] != '\n' && data[pos] != '\r' {
			pos++
		}
		return pos, nil
	case '*':
		for i := pos + 2; i+1 < len(data); i++ {
			if data[i] == '*' && data[i+1] == '/' {
				return i + 2, nil
			}
		}
		return 0, fmt.Errorf("unterminated block comment")
	default:
		return pos, nil
	}
}

func isFTBSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t'
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func pointerKey(locator string) string {
	key := strings.TrimPrefix(locator, "/")
	key = strings.ReplaceAll(key, "~1", "/")
	key = strings.ReplaceAll(key, "~0", "~")
	return key
}

func kubeJSNamespace(sourceFile string) string {
	parts := strings.Split(filepath.ToSlash(sourceFile), "/")
	if len(parts) == 6 && parts[0] == "sources" && parts[1] == "kubejs" && parts[2] == "assets" && parts[4] == "lang" {
		return parts[3]
	}
	return ""
}

func orderedJSONKeys(data []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("root must be an object")
	}
	keys := []string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("object key token is %T", token)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple root values")
		}
		return nil, err
	}
	return keys, nil
}

func marshalOrderedStringObject(order []string, values map[string]string) ([]byte, error) {
	var builder strings.Builder
	builder.WriteString("{\n")
	written := map[string]bool{}
	first := true
	writePair := func(key, value string) error {
		if !first {
			builder.WriteString(",\n")
		}
		first = false
		keyData, err := json.Marshal(key)
		if err != nil {
			return err
		}
		valueData, err := json.Marshal(value)
		if err != nil {
			return err
		}
		builder.WriteString("  ")
		builder.Write(keyData)
		builder.WriteString(": ")
		builder.Write(valueData)
		written[key] = true
		return nil
	}
	for _, key := range order {
		value, ok := values[key]
		if !ok {
			continue
		}
		if err := writePair(key, value); err != nil {
			return nil, err
		}
	}
	extra := make([]string, 0, len(values))
	for key := range values {
		if !written[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		if err := writePair(key, values[key]); err != nil {
			return nil, err
		}
	}
	builder.WriteString("\n}\n")
	return []byte(builder.String()), nil
}

func extractNamespace(sourceFile string) string {
	sf := filepath.ToSlash(sourceFile)
	parts := strings.Split(sf, "/")
	if len(parts) >= 2 && parts[0] == "assets" {
		return parts[1]
	}
	return ""
}

func loadCatalog(path string) (*catalogV1, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var catalog catalogV1
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &catalog, nil
}

func loadTranslationCache(path string) (*translationCacheV2, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cache translationCacheV2
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cache, nil
}
