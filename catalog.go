package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tnze/go-mc/nbt"
	json5 "github.com/titanous/json5"
	"modpack-translator/tokenprotect"
)

const (
	catalogSchema       = "modpack-translator.catalog/v1"
	maxCatalogDepth     = 64
	maxCatalogMembers   = 100000
	maxCatalogEntries   = 100000
	maxCatalogText      = 64 << 10
	maxCatalogAggregate = 256 << 20
)

type CatalogV1 struct {
	Schema       string           `json:"schema"`
	SourceLocale string           `json:"source_locale"`
	TargetLocale string           `json:"target_locale"`
	Entries      []CatalogEntryV1 `json:"entries"`
}

type CatalogEntryV1 struct {
	ID           string             `json:"id"`
	SourceKind   string             `json:"source_kind"`
	SourceFile   string             `json:"source_file"`
	Locator      string             `json:"locator"`
	Source       string             `json:"source"`
	TargetLocale string             `json:"target_locale"`
	Tokens       []CatalogTokenV1   `json:"tokens"`
	Writeback    CatalogWritebackV1 `json:"writeback"`
}

type CatalogTokenV1 struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type CatalogWritebackV1 struct {
	Format       string `json:"format"`
	ValueType    string `json:"value_type"`
	Container    string `json:"container"`
	ArrayIndex   *int   `json:"array_index,omitempty"`
	SourceSHA256 string `json:"source_file_sha256"`
	Encoding     string `json:"encoding"`
}

type catalogBuilder struct {
	workspace string
	entries   []CatalogEntryV1
	aggregate int64
}

func buildCatalog(workspace string) (int, string, error) {
	b := &catalogBuilder{workspace: workspace}
	if err := b.extractLang(); err != nil {
		return 0, "", err
	}
	if err := b.extractKubeJSLang(); err != nil {
		return 0, "", err
	}
	if err := b.extractPatchouli(); err != nil {
		return 0, "", err
	}
	if err := b.extractFTB(); err != nil {
		return 0, "", err
	}
	sort.Slice(b.entries, func(i, j int) bool { return catalogEntryLess(b.entries[i], b.entries[j]) })
	seen := make(map[string]CatalogEntryV1, len(b.entries))
	for _, entry := range b.entries {
		if previous, ok := seen[entry.ID]; ok {
			if !catalogEntriesEqual(previous, entry) {
				return 0, "", fmt.Errorf("catalog stable ID collision %s", entry.ID)
			}
			return 0, "", fmt.Errorf("duplicate catalog occurrence %s at %s%s", entry.ID, entry.SourceFile, entry.Locator)
		}
		seen[entry.ID] = entry
	}
	catalog := CatalogV1{Schema: catalogSchema, SourceLocale: sourceLanguageCode, TargetLocale: targetLanguageCode, Entries: b.entries}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return 0, "", fmt.Errorf("marshal catalog: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Join(workspace, "catalog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, "", fmt.Errorf("create catalog directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".catalog.v1-*.tmp")
	if err != nil {
		return 0, "", fmt.Errorf("create catalog staging file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return 0, "", fmt.Errorf("write catalog staging file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, "", fmt.Errorf("sync catalog staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", fmt.Errorf("close catalog staging file: %w", err)
	}
	destination := filepath.Join(dir, "catalog.v1.json")
	if err := replaceFile(tmpName, destination); err != nil {
		return 0, "", fmt.Errorf("publish catalog: %w", err)
	}
	return len(b.entries), destination, nil
}

func replaceFile(staged, destination string) error {
	backup := destination + ".previous"
	_ = os.Remove(backup)
	hadPrevious := false
	if err := os.Rename(destination, backup); err == nil {
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, destination); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	if hadPrevious {
		_ = os.Remove(backup)
	}
	return nil
}

func (b *catalogBuilder) extractLang() error {
	root := filepath.Join(b.workspace, "assets")
	return walkSelected(root, func(relative string) bool {
		parts := strings.Split(filepath.ToSlash(relative), "/")
		return len(parts) == 3 && parts[1] == "lang" && parts[2] == pendingTranslationFileName()
	}, func(filePath, relative string, data []byte) error {
		var object map[string]json.RawMessage
		if err := decodeJSONObjectUnique(data, &object); err != nil {
			return fmt.Errorf("parse standard lang %s: %w", filePath, err)
		}
		for key, raw := range object {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("standard lang %s key %q must be a string", filePath, key)
			}
			if strings.TrimFunc(value, unicode.IsSpace) == "" {
				continue
			}
			if err := b.add("standard_lang", filepath.ToSlash(filepath.Join("assets", relative)), "/"+escapePointer(key), value, "json", "string", "object", nil, data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *catalogBuilder) extractKubeJSLang() error {
	root := filepath.Join(b.workspace, "sources", "kubejs", "assets")
	return walkSelected(root, func(relative string) bool {
		parts := strings.Split(filepath.ToSlash(relative), "/")
		return len(parts) == 3 && parts[0] != "" && parts[1] == "lang" && parts[2] == sourceLanguageCode+".json"
	}, func(filePath, relative string, data []byte) error {
		var object map[string]json.RawMessage
		if err := decodeJSONObjectUnique(data, &object); err != nil {
			return fmt.Errorf("parse KubeJS lang %s: %w", filePath, err)
		}
		sourceFile := filepath.ToSlash(filepath.Join("sources", "kubejs", "assets", relative))
		for key, raw := range object {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("KubeJS lang %s key %q must be a string", filePath, key)
			}
			if strings.TrimFunc(value, unicode.IsSpace) == "" {
				continue
			}
			if err := b.add("kubejs_lang", sourceFile, "/"+escapePointer(key), value, "json", "string", "object", nil, data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *catalogBuilder) extractPatchouli() error {
	root := filepath.Join(b.workspace, "sources", "patchouli")
	type file struct {
		filePath, relative string
		data               []byte
	}
	var files []file
	if err := walkSelected(root, func(relative string) bool { return strings.EqualFold(filepath.Ext(relative), ".json") }, func(p, r string, d []byte) error {
		files = append(files, file{p, filepath.ToSlash(filepath.Join("sources", "patchouli", r)), d})
		return nil
	}); err != nil {
		return err
	}
	i18nBooks := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f.relative, "/book.json") {
			continue
		}
		var object map[string]json.RawMessage
		if decodeJSONObjectUnique(f.data, &object) == nil {
			var enabled bool
			if raw, ok := object["i18n"]; ok && json.Unmarshal(raw, &enabled) == nil && enabled {
				i18nBooks[patchouliBookKey(f.relative)] = true
			}
		}
	}
	for _, f := range files {
		var object map[string]any
		if err := checkJSON5Structure(f.data); err != nil {
			return fmt.Errorf("validate Patchouli %s: %w", f.filePath, err)
		}
		if err := decodeJSONUniqueAny(f.data, &object); err != nil {
			return fmt.Errorf("parse Patchouli %s: %w", f.filePath, err)
		}
		if err := validateTree(object, 1); err != nil {
			return fmt.Errorf("validate Patchouli %s: %w", f.filePath, err)
		}
		skipKeys := i18nBooks[patchouliBookKey(f.relative)]
		kind := patchouliFileKind(f.relative)
		fields := []string{}
		switch kind {
		case "book":
			fields = []string{"name", "landing_text", "subtitle"}
		case "category":
			fields = []string{"name", "description"}
		case "entry":
			fields = []string{"name"}
		}
		for _, field := range fields {
			if value, ok := object[field].(string); ok && !skipKeys {
				if err := b.add("patchouli", f.relative, "/"+field, value, "json", "string", "object", nil, f.data); err != nil {
					return err
				}
			}
		}
		if kind == "entry" {
			if pages, ok := object["pages"].([]any); ok {
				for i, pageValue := range pages {
					base := "/pages/" + strconv.Itoa(i)
					if text, ok := pageValue.(string); ok && !skipKeys {
						index := i
						if err := b.add("patchouli", f.relative, base, text, "json", "string", "array", &index, f.data); err != nil {
							return err
						}
						continue
					}
					pageObject, ok := pageValue.(map[string]any)
					if !ok {
						continue
					}
					pageType, _ := pageObject["type"].(string)
					allowed := []string{"title", "text"}
					if pageType == "patchouli:multiblock" || pageType == "patchouli:entity" {
						allowed = append(allowed, "name")
					}
					if pageType == "patchouli:link" {
						allowed = append(allowed, "link_text")
					}
					if !isBuiltinPatchouliPage(pageType) {
						continue
					}
					for _, field := range allowed {
						if text, ok := pageObject[field].(string); ok && !skipKeys {
							if err := b.add("patchouli", f.relative, base+"/"+field, text, "json", "string", "object", nil, f.data); err != nil {
								return err
							}
						}
					}
				}
			}
		}
		if kind == "template" && !skipKeys {
			if err := b.extractTemplate(f.relative, object, f.data); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *catalogBuilder) extractTemplate(file string, object map[string]any, data []byte) error {
	components, ok := object["components"].([]any)
	if !ok {
		return nil
	}
	for i, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		base := "/components/" + strconv.Itoa(i)
		if value, ok := component["text"].(string); ok && !strings.HasPrefix(value, "#") {
			if err := b.add("patchouli", file, base+"/text", value, "json", "string", "object", nil, data); err != nil {
				return err
			}
		}
		if tooltip, ok := component["tooltip"].([]any); ok {
			for j, rawText := range tooltip {
				if text, ok := rawText.(string); ok && !strings.HasPrefix(text, "#") {
					index := j
					if err := b.add("patchouli", file, base+"/tooltip/"+strconv.Itoa(j), text, "json", "string", "array", &index, data); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (b *catalogBuilder) extractFTB() error {
	root := filepath.Join(b.workspace, "sources", "ftbquests")
	return walkSelected(root, func(relative string) bool {
		ext := strings.ToLower(filepath.Ext(relative))
		return ext == ".snbt" || ext == ".json5"
	}, func(filePath, relative string, data []byte) error {
		sourceFile := filepath.ToSlash(filepath.Join("sources", "ftbquests", relative))
		switch strings.ToLower(filepath.Ext(relative)) {
		case ".snbt":
			return b.extractSNBT(sourceFile, relative, data)
		case ".json5":
			return b.extractJSON5(sourceFile, data)
		}
		return nil
	})
}

func (b *catalogBuilder) extractSNBT(sourceFile, relative string, data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("SNBT %s is not UTF-8", sourceFile)
	}
	normalized, err := normalizeFTBQuestSNBT(data)
	if err != nil {
		return fmt.Errorf("normalize FTB quest SNBT %s: %w", sourceFile, err)
	}
	var binaryNBT bytes.Buffer
	binaryNBT.WriteByte(nbt.TagCompound)
	binaryNBT.Write([]byte{0, 0})
	if err := nbt.StringifiedMessage(normalized).MarshalNBT(&binaryNBT); err != nil {
		return fmt.Errorf("parse SNBT %s: %w", sourceFile, err)
	}
	dir := path.Base(path.Dir(filepath.ToSlash(relative)))
	type titled struct {
		Title string `nbt:"title"`
	}
	type image struct {
		Hover ftbHover `nbt:"hover"`
	}
	type questFile struct {
		Title       string   `nbt:"title"`
		Subtitle    string   `nbt:"subtitle"`
		Description []string `nbt:"description"`
		Tasks       []titled `nbt:"tasks"`
		Rewards     []titled `nbt:"rewards"`
	}
	root := map[string]any{}
	if dir == "chapters" {
		var decoded struct {
			Title    string   `nbt:"title"`
			Subtitle []string `nbt:"subtitle"`
			Images   []image  `nbt:"images"`
		}
		if _, err := nbt.NewDecoder(&binaryNBT).Decode(&decoded); err != nil {
			return fmt.Errorf("decode SNBT %s: %w", sourceFile, err)
		}
		root["title"] = decoded.Title
		root["subtitle"] = stringsToAny(decoded.Subtitle)
		images := make([]any, len(decoded.Images))
		for i, image := range decoded.Images {
			images[i] = map[string]any{"hover": stringsToAny(image.Hover)}
		}
		root["images"] = images
	} else if dir == "quests" {
		var decoded questFile
		if _, err := nbt.NewDecoder(&binaryNBT).Decode(&decoded); err != nil {
			return fmt.Errorf("decode SNBT %s: %w", sourceFile, err)
		}
		root = map[string]any{
			"title": decoded.Title, "subtitle": decoded.Subtitle, "description": stringsToAny(decoded.Description),
		}
		for _, pair := range []struct {
			name   string
			values []titled
		}{{"tasks", decoded.Tasks}, {"rewards", decoded.Rewards}} {
			items := make([]any, len(pair.values))
			for i, value := range pair.values {
				items[i] = map[string]any{"title": value.Title}
			}
			root[pair.name] = items
		}
	} else if dir == "toasts" {
		var decoded struct {
			Description []string `nbt:"description"`
		}
		if _, err := nbt.NewDecoder(&binaryNBT).Decode(&decoded); err != nil {
			return fmt.Errorf("decode SNBT %s: %w", sourceFile, err)
		}
		root["description"] = stringsToAny(decoded.Description)
	} else {
		var decoded struct {
			Title       string `nbt:"title"`
			LockMessage string `nbt:"lock_message"`
		}
		if _, err := nbt.NewDecoder(&binaryNBT).Decode(&decoded); err != nil {
			return fmt.Errorf("decode SNBT %s: %w", sourceFile, err)
		}
		root["title"] = decoded.Title
		root["lock_message"] = decoded.LockMessage
	}
	if err := validateTree(root, 1); err != nil {
		return fmt.Errorf("validate SNBT %s: %w", sourceFile, err)
	}
	fields := map[string][]string{
		"quests": {"title", "subtitle", "description"}, "chapters": {"title", "subtitle"}, "chapter_groups": {"title"},
		"tasks": {"title"}, "rewards": {"title"}, "reward_tables": {"title"}, "toasts": {"description"},
	}
	allowed := fields[dir]
	if path.Base(relative) == "data.snbt" {
		allowed = []string{"title", "lock_message"}
	}
	for _, field := range allowed {
		if err := b.addSNBTValue(sourceFile, root[field], "/"+field, data); err != nil {
			return err
		}
	}
	if dir == "quests" {
		for _, collection := range []string{"tasks", "rewards"} {
			if list, ok := root[collection].([]any); ok {
				for i, raw := range list {
					if object, ok := raw.(map[string]any); ok {
						if err := b.addSNBTValue(sourceFile, object["title"], "/"+collection+"/"+strconv.Itoa(i)+"/title", data); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if dir == "chapters" {
		if images, ok := root["images"].([]any); ok {
			for i, raw := range images {
				if object, ok := raw.(map[string]any); ok {
					if err := b.addSNBTValue(sourceFile, object["hover"], "/images/"+strconv.Itoa(i)+"/hover", data); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

type ftbHover []string

func (h *ftbHover) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	var raw nbt.RawMessage
	if err := raw.UnmarshalNBT(tagType, r); err != nil {
		return err
	}
	switch tagType {
	case nbt.TagString:
		var value string
		if err := raw.Unmarshal(&value); err != nil {
			return err
		}
		*h = []string{value}
	case nbt.TagList:
		var values []string
		if err := raw.Unmarshal(&values); err != nil {
			return err
		}
		*h = values
	default:
		return fmt.Errorf("cannot parse Tag %#02x as FTB image hover", tagType)
	}
	return nil
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func (b *catalogBuilder) addSNBTValue(file string, value any, locator string, data []byte) error {
	switch value := value.(type) {
	case string:
		if skipFTBText(value) {
			return nil
		}
		return b.add("ftbquests_snbt", file, locator, value, "snbt", "string", "object", nil, data)
	case []any:
		for i, raw := range value {
			text, ok := raw.(string)
			if !ok {
				return fmt.Errorf("SNBT %s %s must contain strings only", file, locator)
			}
			if skipFTBText(text) {
				continue
			}
			index := i
			if err := b.add("ftbquests_snbt", file, locator+"/"+strconv.Itoa(i), text, "snbt", "string", "array", &index, data); err != nil {
				return err
			}
		}
	}
	return nil
}

var ftbLocalizationReference = regexp.MustCompile(`^\{ftbquests\.[a-z0-9_.-]+\}$`)

func skipFTBText(value string) bool {
	return strings.TrimFunc(value, unicode.IsSpace) == "" || value == "{@pagebreak}" || ftbLocalizationReference.MatchString(value)
}

var ftbLocaleFlat = regexp.MustCompile(`^(title|quest_subtitle|quest_desc|chapter_subtitle)[._]([0-9a-fA-F]{16})$`)

func (b *catalogBuilder) extractJSON5(sourceFile string, data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON5 %s is not UTF-8", sourceFile)
	}
	if err := checkJSON5Structure(data); err != nil {
		return fmt.Errorf("validate JSON5 %s: %w", sourceFile, err)
	}
	var root map[string]any
	if err := json5.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse JSON5 %s: %w", sourceFile, err)
	}
	if err := validateTree(root, 1); err != nil {
		return fmt.Errorf("validate JSON5 %s: %w", sourceFile, err)
	}
	for key, value := range root {
		if ftbLocaleFlat.MatchString(key) {
			if err := b.addJSON5Value(sourceFile, value, "/"+escapePointer(key), data); err != nil {
				return err
			}
			continue
		}
		if !isFTBLocaleField(key) {
			continue
		}
		table, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("JSON5 %s field %q must be an object", sourceFile, key)
		}
		for id, text := range table {
			if !regexp.MustCompile(`^[0-9a-fA-F]{16}$`).MatchString(id) {
				continue
			}
			if err := b.addJSON5Value(sourceFile, text, "/"+escapePointer(key)+"/"+id, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *catalogBuilder) addJSON5Value(file string, value any, locator string, data []byte) error {
	switch value := value.(type) {
	case string:
		if skipFTBText(value) {
			return nil
		}
		return b.add("ftbquests_json5", file, locator, value, "json5", "string", "object", nil, data)
	case []any:
		for i, raw := range value {
			text, ok := raw.(string)
			if !ok {
				return fmt.Errorf("JSON5 %s %s must contain strings only", file, locator)
			}
			if skipFTBText(text) {
				continue
			}
			index := i
			if err := b.add("ftbquests_json5", file, locator+"/"+strconv.Itoa(i), text, "json5", "string", "array", &index, data); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("JSON5 %s %s must be a string or string array", file, locator)
	}
	return nil
}

func (b *catalogBuilder) add(kind, sourceFile, locator, source, format, valueType, container string, arrayIndex *int, fileData []byte) error {
	clean, err := normalizeWorkspacePath(sourceFile)
	if err != nil {
		return err
	}
	if !utf8.ValidString(source) {
		return fmt.Errorf("%s%s contains invalid UTF-8", clean, locator)
	}
	if len(source) > maxCatalogText {
		return fmt.Errorf("%s%s text is %d bytes; limit is %d", clean, locator, len(source), maxCatalogText)
	}
	if len(b.entries)+1 > maxCatalogEntries {
		return fmt.Errorf("catalog entry count exceeds %d", maxCatalogEntries)
	}
	b.aggregate += int64(len(source))
	if b.aggregate > maxCatalogAggregate {
		return fmt.Errorf("catalog source text exceeds %d bytes", maxCatalogAggregate)
	}
	digest := sha256.Sum256(fileData)
	tokens := tokenprotect.Find(source)
	metadata := make([]CatalogTokenV1, len(tokens))
	for i, token := range tokens {
		metadata[i] = CatalogTokenV1{Kind: string(token.Kind), Text: token.Text, Start: token.Start, End: token.End}
	}
	b.entries = append(b.entries, CatalogEntryV1{
		ID: stableCatalogID(kind, clean, locator, targetLanguageCode), SourceKind: kind, SourceFile: clean, Locator: locator, Source: source, TargetLocale: targetLanguageCode, Tokens: metadata,
		Writeback: CatalogWritebackV1{Format: format, ValueType: valueType, Container: container, ArrayIndex: arrayIndex, SourceSHA256: hex.EncodeToString(digest[:]), Encoding: "UTF-8"},
	})
	return nil
}

func stableCatalogID(kind, file, locator, locale string) string {
	hash := sha256.New()
	for _, value := range []string{catalogSchema, kind, file, locator, locale} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeWorkspacePath(value string) (string, error) {
	value = filepath.ToSlash(value)
	if value == "" || path.IsAbs(value) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", fmt.Errorf("source path %q must be workspace-relative", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fmt.Errorf("source path %q escapes or is not normalized", value)
	}
	return clean, nil
}

func decodeJSONObjectUnique(data []byte, destination *map[string]json.RawMessage) error {
	if !utf8.Valid(data) {
		return errors.New("file is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("root must be an object")
	}
	result := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key := keyToken.(string)
		if _, exists := result[key]; exists {
			return fmt.Errorf("duplicate object key %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		result[key] = raw
		if len(result) > maxCatalogMembers {
			return fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	*destination = result
	return nil
}

func decodeJSONUniqueAny(data []byte, destination *map[string]any) error {
	var raw map[string]json.RawMessage
	if err := decodeJSONObjectUnique(data, &raw); err != nil {
		return err
	}
	result := make(map[string]any, len(raw))
	for key, value := range raw {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return err
		}
		result[key] = decoded
	}
	*destination = result
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("multiple root values")
		}
		return err
	}
	return nil
}

func validateTree(value any, depth int) error {
	if depth > maxCatalogDepth {
		return fmt.Errorf("nesting exceeds %d", maxCatalogDepth)
	}
	switch value := value.(type) {
	case map[string]any:
		if len(value) > maxCatalogMembers {
			return fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
		for _, child := range value {
			if err := validateTree(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(value) > maxCatalogMembers {
			return fmt.Errorf("array exceeds %d members", maxCatalogMembers)
		}
		for _, child := range value {
			if err := validateTree(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkSelected(root string, selectFile func(string) bool, visit func(string, string, []byte) error) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxPendingSourceFileSize {
			if info.Size() > maxPendingSourceFileSize {
				return fmt.Errorf("source file %s exceeds %d bytes", filePath, maxPendingSourceFileSize)
			}
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		if !selectFile(relative) {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		return visit(filePath, relative, data)
	})
}

func patchouliFileKind(file string) string {
	switch {
	case strings.HasSuffix(file, "/book.json"):
		return "book"
	case strings.Contains(file, "/categories/"):
		return "category"
	case strings.Contains(file, "/entries/"):
		return "entry"
	case strings.Contains(file, "/templates/"):
		return "template"
	}
	return ""
}
func patchouliBookKey(file string) string {
	parts := strings.Split(file, "/")
	jarPrefix := ""
	for i, part := range parts {
		if part == "jars" && i+1 < len(parts) {
			jarPrefix = strings.Join(parts[:i+2], "/")
			break
		}
	}
	for i, part := range parts {
		if part == "patchouli_books" && i+1 < len(parts) {
			namespace := ""
			if i > 0 {
				namespace = parts[i-1]
			}
			if jarPrefix != "" {
				return jarPrefix + "/" + namespace + "/" + parts[i+1]
			}
			return strings.Join(parts[:i+2], "/")
		}
	}
	if i := strings.Index(file, "/en_us/"); i >= 0 {
		return file[:i]
	}
	return path.Dir(file)
}
func isBuiltinPatchouliPage(value string) bool {
	if value == "" || strings.HasPrefix(value, "#") {
		return false
	}
	if strings.Contains(value, ":") {
		return strings.HasPrefix(value, "patchouli:")
	}
	return true
}
func isFTBLocaleField(value string) bool {
	return value == "title" || value == "quest_subtitle" || value == "quest_desc" || value == "chapter_subtitle"
}
func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func catalogEntryLess(a, b CatalogEntryV1) bool {
	rank := map[string]int{"standard_lang": 0, "kubejs_lang": 1, "patchouli": 2, "ftbquests_snbt": 3, "ftbquests_json5": 4}
	if rank[a.SourceKind] != rank[b.SourceKind] {
		return rank[a.SourceKind] < rank[b.SourceKind]
	}
	if a.SourceFile != b.SourceFile {
		return a.SourceFile < b.SourceFile
	}
	return compareLocator(a.Locator, b.Locator) < 0
}
func compareLocator(a, b string) int {
	aa, bb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		ai, ae := strconv.Atoi(aa[i])
		bi, be := strconv.Atoi(bb[i])
		if ae == nil && be == nil {
			if ai < bi {
				return -1
			}
			if ai > bi {
				return 1
			}
		} else if aa[i] < bb[i] {
			return -1
		} else if aa[i] > bb[i] {
			return 1
		}
	}
	if len(aa) < len(bb) {
		return -1
	}
	if len(aa) > len(bb) {
		return 1
	}
	return 0
}
func catalogEntriesEqual(a, b CatalogEntryV1) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

// checkJSON5Structure rejects duplicate members and enforces structural limits before map decoding.
func checkJSON5Structure(data []byte) error {
	parser := json5StructureParser{data: data}
	if err := parser.value(1); err != nil {
		return err
	}
	parser.space()
	if parser.at != len(data) {
		return fmt.Errorf("unexpected data at byte %d", parser.at)
	}
	return nil
}

type json5StructureParser struct {
	data []byte
	at   int
}

func (p *json5StructureParser) space() {
	for p.at < len(p.data) {
		if strings.ContainsRune(" \t\r\n\v\f", rune(p.data[p.at])) {
			p.at++
			continue
		}
		if p.at+1 < len(p.data) && p.data[p.at] == '/' && p.data[p.at+1] == '/' {
			p.at += 2
			for p.at < len(p.data) && p.data[p.at] != '\n' {
				p.at++
			}
			continue
		}
		if p.at+1 < len(p.data) && p.data[p.at] == '/' && p.data[p.at+1] == '*' {
			end := bytes.Index(p.data[p.at+2:], []byte("*/"))
			if end < 0 {
				p.at = len(p.data)
				return
			}
			p.at += end + 4
			continue
		}
		break
	}
}
func (p *json5StructureParser) value(depth int) error {
	if depth > maxCatalogDepth {
		return fmt.Errorf("nesting exceeds %d", maxCatalogDepth)
	}
	p.space()
	if p.at >= len(p.data) {
		return errors.New("unexpected end")
	}
	switch p.data[p.at] {
	case '{':
		return p.object(depth)
	case '[':
		return p.array(depth)
	case '\'', '"':
		_, err := p.token()
		return err
	default:
		_, err := p.token()
		return err
	}
}
func (p *json5StructureParser) object(depth int) error {
	p.at++
	seen := map[string]bool{}
	count := 0
	p.space()
	if p.take('}') {
		return nil
	}
	for {
		key, err := p.token()
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("duplicate object key %q", key)
		}
		seen[key] = true
		count++
		if count > maxCatalogMembers {
			return fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
		p.space()
		if !p.take(':') {
			return fmt.Errorf("expected colon at byte %d", p.at)
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.space()
		if p.take('}') {
			return nil
		}
		if !p.take(',') {
			return fmt.Errorf("expected comma at byte %d", p.at)
		}
		p.space()
		if p.take('}') {
			return nil
		}
	}
}
func (p *json5StructureParser) array(depth int) error {
	p.at++
	count := 0
	p.space()
	if p.take(']') {
		return nil
	}
	for {
		count++
		if count > maxCatalogMembers {
			return fmt.Errorf("array exceeds %d members", maxCatalogMembers)
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.space()
		if p.take(']') {
			return nil
		}
		if !p.take(',') {
			return fmt.Errorf("expected comma at byte %d", p.at)
		}
		p.space()
		if p.take(']') {
			return nil
		}
	}
}
func (p *json5StructureParser) token() (string, error) {
	p.space()
	if p.at >= len(p.data) {
		return "", errors.New("unexpected end")
	}
	start := p.at
	if quote := p.data[p.at]; quote == '\'' || quote == '"' {
		p.at++
		for p.at < len(p.data) {
			if p.data[p.at] == '\\' {
				p.at += 2
				continue
			}
			if p.at < len(p.data) && p.data[p.at] == quote {
				p.at++
				var value string
				if err := json5.Unmarshal(p.data[start:p.at], &value); err != nil {
					return "", err
				}
				return value, nil
			}
			p.at++
		}
		return "", errors.New("unterminated string")
	}
	for p.at < len(p.data) && !strings.ContainsRune("{}[],: \t\r\n\v\f", rune(p.data[p.at])) {
		if p.at+1 < len(p.data) && p.data[p.at] == '/' && (p.data[p.at+1] == '/' || p.data[p.at+1] == '*') {
			break
		}
		p.at++
	}
	if p.at == start {
		return "", fmt.Errorf("expected token at byte %d", p.at)
	}
	return string(p.data[start:p.at]), nil
}
func (p *json5StructureParser) take(value byte) bool {
	if p.at < len(p.data) && p.data[p.at] == value {
		p.at++
		return true
	}
	return false
}
