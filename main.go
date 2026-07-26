package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"modpack-translator/internal/writeback"
)

const (
	resourcePackName          = "ModpackTranslations"
	outputDirectory           = "modpack-translator-output"
	sourceLanguageCode        = "en_us"
	defaultTargetLanguageCode = "es_es"
	targetLanguageCode        = defaultTargetLanguageCode
	pendingFileSuffix         = ".pending"
)

type PackMeta struct {
	Pack PackInfo `json:"pack"`
}

type PackInfo struct {
	PackFormat  int    `json:"pack_format"`
	Description string `json:"description"`
}

func main() {
	if err := loadExecutableEnv(); err != nil {
		exitError(err)
	}
	if err := run(os.Args[1:]); err != nil {
		exitError(err)
	}
}

func run(args []string) error {
	return runWithLanguageLimits(args, defaultLanguageLimits())
}

func runWithLanguageLimits(args []string, limits languageLimits) (err error) {
	options, err := parseCLI(args)
	if err != nil {
		return err
	}
	pathArgs := []string{}
	if options.modpackPath != "" {
		pathArgs = append(pathArgs, options.modpackPath)
	}
	modpackPath, err := modpackPathFromArgs(pathArgs)
	if err != nil {
		return err
	}
	targetLocale, err := selectTargetLocale(os.Stdin, os.Stdout, os.Getenv)
	if err != nil {
		return err
	}
	logSession, err := startSessionLog(modpackPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			logSession.writeError(err)
		}
		_ = logSession.close()
	}()
	fmt.Printf("Session log: %s\n", logSession.path)
	if options.translate {
		if err := writeback.RemoveStaleZip(modpackPath); err != nil {
			return err
		}
	}

	modsPath := filepath.Join(modpackPath, "mods")
	workspacePath, exportPackPath := outputPaths(modpackPath)

	minecraftVersion, err := detectMinecraftVersion(modpackPath)
	if err != nil {
		return err
	}
	packFormat, err := resourcePackFormat(minecraftVersion)
	if err != nil {
		return err
	}
	fmt.Printf("Minecraft detected: %s (resource pack format %d)\n", minecraftVersion, packFormat)

	if err := prepareOutputs(workspacePath, exportPackPath, minecraftVersion, packFormat, targetLocale); err != nil {
		return err
	}

	jarFiles, err := filepath.Glob(filepath.Join(modsPath, "*.jar"))
	if err != nil {
		return err
	}

	fmt.Printf("Found %d mods\n", len(jarFiles))
	fmt.Printf("Refresh mode: %s\n", enabledDisabled(options.refresh))
	extractor, err := newSourceExtractor(workspacePath, targetLocale)
	if err != nil {
		return err
	}
	extractor.refresh = options.refresh
	defer extractor.abort()

	languages := newLanguageAggregatorWithLimits(extractor.stageWorkspace, limits, targetLocale)
	languages.refresh = options.refresh
	languages.force = options.force
	for _, jarPath := range jarFiles {
		if err := languages.addJar(jarPath); err != nil {
			return fmt.Errorf("extract standard language from %s: %w", filepath.Base(jarPath), err)
		}
		if err := extractor.extractPatchouliJar(jarPath); err != nil {
			return fmt.Errorf("extract Patchouli from %s: %w", filepath.Base(jarPath), err)
		}
	}
	if err := languages.publish(); err != nil {
		return fmt.Errorf("publish standard languages: %w", err)
	}
	if err := extractor.extractInstancePatchouli(modpackPath); err != nil {
		return fmt.Errorf("extract instance Patchouli: %w", err)
	}
	if err := extractor.extractFTBQuests(modpackPath); err != nil {
		return fmt.Errorf("extract FTB Quests: %w", err)
	}
	if err := extractor.extractKubeJSLang(modpackPath); err != nil {
		return fmt.Errorf("extract KubeJS language files: %w", err)
	}
	entryCount, _, err := buildCatalog(extractor.stageWorkspace, targetLocale)
	if err != nil {
		return fmt.Errorf("build translation catalog: %w", err)
	}
	if err := extractor.commitWorkspace(workspacePath); err != nil {
		return err
	}
	catalogPath := filepath.Join(workspacePath, "catalog", "catalog.v1.json")

	fmt.Printf("Standard sources: processed %d JARs\n", len(jarFiles))
	fmt.Printf("Patchouli sources: %d files\n", extractor.counts.patchouli)
	fmt.Printf("FTB Quests sources: %d files\n", extractor.counts.ftbquests)
	fmt.Printf("KubeJS sources: %d files\n", extractor.counts.kubejs)
	fmt.Printf("Translation catalog: %d entries\n%s\n", entryCount, catalogPath)

	fmt.Printf("\nTranslation workspace (pending files):\n%s\n", workspacePath)
	fmt.Printf("\nExport resource pack (metadata and completed translations only):\n%s\n", exportPackPath)
	if options.translate {
		translator, model, err := buildTranslatorChain(os.Getenv)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s\nTranslating catalog with the configured provider chain\n", providerChainSummary(os.Getenv))
		if err := translateWorkspace(context.Background(), workspacePath, model, translator, translationOptions{}); err != nil {
			return err
		}
		fmt.Printf("Validated translations cached at:\n%s\n", translationCachePath(workspacePath, model, targetLocale))
		if _, err := writebackWorkspace(modpackPath); err != nil {
			return err
		}
	}
	return nil
}

func writebackWorkspace(modpackPath string) (string, error) {
	return writeback.Workspace(modpackPath)
}

func outputPaths(modpackPath string) (workspacePath, exportPackPath string) {
	root := filepath.Join(modpackPath, outputDirectory)
	return filepath.Join(root, "workspace"), filepath.Join(root, "export", "overrides", "resourcepacks", resourcePackName)
}

func prepareOutputs(workspacePath, exportPackPath, minecraftVersion string, packFormat int, targetLocale ...string) error {
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return fmt.Errorf("create translation workspace: %w", err)
	}
	if err := removePendingExportFiles(exportPackPath, targetLocale...); err != nil {
		return err
	}
	return createPackMetadata(exportPackPath, minecraftVersion, packFormat)
}

func removePendingExportFiles(exportPackPath string, targetLocale ...string) error {
	pendingName := pendingTranslationFileName(targetLocale...)
	err := filepath.WalkDir(exportPackPath, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(exportPackPath, path)
		if err != nil {
			return err
		}
		_, language, isLanguageFile := languageFile(filepath.ToSlash(relativePath))
		if isLanguageFile && language == pendingName {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale pending export file: %w", err)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clean pending export files: %w", err)
	}
	return nil
}

func modpackPathFromArgs(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	currentDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read current path: %w", err)
	}
	if hasModsDirectory(currentDirectory) {
		fmt.Printf("Modpack detected: %s\n", currentDirectory)
		return currentDirectory, nil
	}

	candidates, err := findCommonModpacks()
	if err != nil {
		return "", err
	}

	if len(candidates) == 1 {
		fmt.Printf("Modpack detected: %s\n", candidates[0])
		return candidates[0], nil
	}

	if len(candidates) == 0 {
		return "", errors.New("usage: minecraft-translator <modpack-path>\nNo modpacks found in the current path or common installation paths")
	}

	return selectModpack(candidates, os.Stdin, os.Stdout)
}

func selectModpack(candidates []string, input io.Reader, output io.Writer) (string, error) {
	fmt.Fprintln(output, "Several modpacks were found:")
	for i, candidate := range candidates {
		fmt.Fprintf(output, "%d. %s\n", i+1, candidate)
	}
	fmt.Fprint(output, "Choose a modpack by number: ")

	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read selection: %w", err)
		}
		return "", errors.New("no selection received")
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(candidates) {
		return "", fmt.Errorf("invalid selection: choose a number between 1 and %d", len(candidates))
	}

	selected := candidates[choice-1]
	fmt.Fprintf(output, "Selected modpack: %s\n", selected)
	return selected, nil
}

func findCommonModpacks() ([]string, error) {
	return findModpacksInRoots(commonModpackSearchRoots())
}

func commonModpackSearchRoots() []string {
	var roots []string
	add := func(path string) {
		if path != "" {
			roots = append(roots, path)
		}
	}
	addFromBase := func(base string, elem ...string) {
		if base != "" {
			add(filepath.Join(append([]string{base}, elem...)...))
		}
	}

	home, _ := os.UserHomeDir()
	config, _ := os.UserConfigDir()
	currentDirectory, _ := os.Getwd()

	add(currentDirectory)
	addFromBase(home, ".minecraft")
	addFromBase(home, "curseforge", "minecraft", "Instances")
	addFromBase(home, "Documents", "Curse", "Minecraft", "Instances")

	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		addFromBase(appData, ".minecraft")
		addFromBase(appData, "PrismLauncher", "instances")
		addFromBase(appData, "PolyMC", "instances")
		addFromBase(appData, "MultiMC", "instances")
		addFromBase(appData, "ATLauncher", "instances")
		addFromBase(appData, "com.modrinth.theseus", "profiles")
	case "darwin":
		addFromBase(home, "Library", "Application Support", "minecraft")
		addFromBase(home, "Library", "Application Support", "PrismLauncher", "instances")
		addFromBase(home, "Library", "Application Support", "PolyMC", "instances")
		addFromBase(home, "Library", "Application Support", "MultiMC", "instances")
		addFromBase(home, "Library", "Application Support", "ATLauncher", "instances")
		addFromBase(home, "Library", "Application Support", "com.modrinth.theseus", "profiles")
	default:
		addFromBase(config, "PrismLauncher", "instances")
		addFromBase(config, "PolyMC", "instances")
		addFromBase(config, "MultiMC", "instances")
		addFromBase(config, "ATLauncher", "instances")
		addFromBase(config, "com.modrinth.theseus", "profiles")
		addFromBase(home, ".local", "share", "PrismLauncher", "instances")
		addFromBase(home, ".local", "share", "PolyMC", "instances")
		addFromBase(home, ".local", "share", "multimc", "instances")
		addFromBase(home, ".var", "app", "org.prismlauncher.PrismLauncher", "data", "PrismLauncher", "instances")
	}

	return roots
}

func findModpacksInRoots(roots []string) ([]string, error) {
	seen := make(map[string]bool)
	var candidates []string

	addCandidate := func(path string) {
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		seen[clean] = true
		candidates = append(candidates, clean)
	}

	for _, root := range roots {
		if root == "" {
			continue
		}
		if hasModsDirectory(root) {
			addCandidate(root)
		}

		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read common installation %s: %w", root, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			instancePath := filepath.Join(root, entry.Name())
			if hasModsDirectory(instancePath) {
				addCandidate(instancePath)
			}

			minecraftPath := filepath.Join(instancePath, ".minecraft")
			if hasModsDirectory(minecraftPath) {
				addCandidate(minecraftPath)
			}
		}
	}

	sort.Strings(candidates)
	return candidates, nil
}

func hasModsDirectory(path string) bool {
	info, err := os.Stat(filepath.Join(path, "mods"))
	return err == nil && info.IsDir()
}

func createPackMetadata(outputPath, minecraftVersion string, packFormat int) error {
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return fmt.Errorf("crear carpeta del resource pack: %w", err)
	}

	meta := PackMeta{
		Pack: PackInfo{
			PackFormat:  packFormat,
			Description: fmt.Sprintf("Modpack translation workspace for Minecraft %s", minecraftVersion),
		},
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("generar pack.mcmeta: %w", err)
	}

	path := filepath.Join(outputPath, "pack.mcmeta")

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("guardar pack.mcmeta: %w", err)
	}

	return nil
}

type languageValue struct {
	value  string
	source string
}

type languageConflict struct {
	first  string
	second string
}

type namespaceLanguages struct {
	targets map[string]bool
	sources []languageSource
}

type languageSource struct {
	jar        string
	values     map[string]string
	duplicates map[string]bool
	nonStrings map[string]bool
}

type parsedSourceLanguage struct {
	values     map[string]string
	duplicates map[string]bool
	nonStrings map[string]bool
	members    int
	empty      bool
}

type languageAggregator struct {
	outputPath   string
	byNamespace  map[string]*namespaceLanguages
	budget       languageBudget
	refresh      bool
	force        bool
	targetLocale string
}

func newLanguageAggregator(outputPath string) *languageAggregator {
	return newLanguageAggregatorWithLimits(outputPath, defaultLanguageLimits(), defaultTargetLanguageCode)
}

type languageLimits struct {
	files   uint64
	bytes   uint64
	members uint64
}

type languageBudget struct {
	limits  languageLimits
	files   uint64
	bytes   uint64
	members uint64
}

func defaultLanguageLimits() languageLimits {
	return languageLimits{files: maxPendingSourceFiles, bytes: maxPendingSourceTotal, members: maxCatalogEntries}
}

func newLanguageAggregatorWithLimits(outputPath string, limits languageLimits, targetLocale ...string) *languageAggregator {
	return &languageAggregator{
		outputPath:   outputPath,
		byNamespace:  make(map[string]*namespaceLanguages),
		budget:       languageBudget{limits: limits},
		targetLocale: selectedTargetLocale(targetLocale...),
	}
}

func (b *languageBudget) accountBytes(size uint64, jarPath, entryName string) error {
	if b.files >= b.limits.files {
		return fmt.Errorf("language file count limit %d exceeded at %s in %s", b.limits.files, entryName, filepath.Base(jarPath))
	}
	if size > b.limits.bytes-b.bytes {
		return fmt.Errorf("language byte limit %d exceeded at %s in %s", b.limits.bytes, entryName, filepath.Base(jarPath))
	}
	b.files++
	b.bytes += size
	return nil
}

func (b *languageBudget) accountMembers(count uint64, jarPath, entryName string) error {
	if count > b.limits.members-b.members {
		return fmt.Errorf("language member limit %d exceeded at %s in %s", b.limits.members, entryName, filepath.Base(jarPath))
	}
	b.members += count
	return nil
}

func processMod(jarPath, outputPath string) error {
	aggregator := newLanguageAggregator(outputPath)
	if err := aggregator.addJar(jarPath); err != nil {
		return err
	}
	return aggregator.publish()
}

func (a *languageAggregator) addJar(jarPath string) error {
	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return fmt.Errorf("abrir JAR: %w", err)
	}
	defer reader.Close()

	type jarLanguages struct {
		sources []parsedSourceLanguage
		targets []map[string]bool
	}
	byNamespace := make(map[string]*jarLanguages)

	for _, file := range reader.File {
		entryName, err := safeArchivePath(file.Name)
		if err != nil {
			return fmt.Errorf("unsafe language JAR entry %q in %s: %w", file.Name, filepath.Base(jarPath), err)
		}
		namespace, language, ok := languageFile(entryName)
		if !ok {
			continue
		}

		files := byNamespace[namespace]
		if files == nil {
			files = &jarLanguages{}
			byNamespace[namespace] = files
		}

		if language != sourceLanguageFileName() && language != targetLanguageFileName(a.targetLocale) {
			continue
		}

		data, err := readLanguageFile(file)
		if err != nil {
			return fmt.Errorf("read %s in %s: %w", file.Name, filepath.Base(jarPath), err)
		}
		if err := a.budget.accountBytes(uint64(len(data)), jarPath, file.Name); err != nil {
			return err
		}
		if language == targetLanguageFileName(a.targetLocale) {
			keys, members, err := collectTargetLanguageKeys(data)
			if err != nil {
				if !a.force {
					return fmt.Errorf("parse %s in %s: %w", file.Name, filepath.Base(jarPath), err)
				}
				fmt.Printf("[WARN] %s/%s: ignoring malformed target %s: %v\n", filepath.Base(jarPath), namespace, file.Name, err)
				continue
			}
			if err := a.budget.accountMembers(uint64(members), jarPath, file.Name); err != nil {
				return err
			}
			files.targets = append(files.targets, keys)
		} else {
			source, err := parseStandardLanguage(data)
			if err != nil {
				return fmt.Errorf("parse %s in %s: %w", file.Name, filepath.Base(jarPath), err)
			}
			if source.empty {
				fmt.Printf("[SKIP] %s: empty %s\n", file.Name, sourceLanguageFileName())
				continue
			}
			if err := a.budget.accountMembers(uint64(source.members), jarPath, file.Name); err != nil {
				return err
			}
			files.sources = append(files.sources, source)
		}
	}

	for namespace, files := range byNamespace {
		namespaceFiles := a.byNamespace[namespace]
		if namespaceFiles == nil {
			namespaceFiles = &namespaceLanguages{targets: make(map[string]bool)}
			a.byNamespace[namespace] = namespaceFiles
		}
		for _, keys := range files.targets {
			for key := range keys {
				namespaceFiles.targets[key] = true
			}
		}
		if len(files.targets) > 0 {
			fmt.Printf("[OK] %s/%s: %s found\n", filepath.Base(jarPath), namespace, targetLanguageFileName(a.targetLocale))
		}
		for _, source := range files.sources {
			namespaceFiles.sources = append(namespaceFiles.sources, languageSource{jar: jarPath, values: source.values, duplicates: source.duplicates, nonStrings: source.nonStrings})
		}
		if len(files.targets) == 0 && len(files.sources) == 0 {
			fmt.Printf("[SKIP] %s/%s: missing both %s and %s\n", filepath.Base(jarPath), namespace, targetLanguageFileName(a.targetLocale), sourceLanguageFileName())
		}
	}

	if len(byNamespace) == 0 {
		fmt.Printf("[SKIP] %s: no language files\n", filepath.Base(jarPath))
	}

	return nil
}

func parseStandardLanguage(data []byte) (parsedSourceLanguage, error) {
	if !utf8.Valid(data) {
		return parsedSourceLanguage{}, errors.New("file is not UTF-8")
	}
	data = trimUTF8BOM(data)
	if strings.TrimSpace(string(data)) == "" {
		return parsedSourceLanguage{empty: true}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return parsedSourceLanguage{}, err
	}
	if token != json.Delim('{') {
		return parsedSourceLanguage{}, errors.New("root must be an object")
	}
	result := parsedSourceLanguage{values: make(map[string]string), duplicates: make(map[string]bool), nonStrings: make(map[string]bool)}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return parsedSourceLanguage{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return parsedSourceLanguage{}, errors.New("object key must be a string")
		}
		var encoded json.RawMessage
		if err := decoder.Decode(&encoded); err != nil {
			return parsedSourceLanguage{}, err
		}
		result.members++
		if result.members > maxCatalogMembers {
			return parsedSourceLanguage{}, fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
		if result.duplicates[key] {
			continue
		}
		if _, exists := result.values[key]; exists {
			delete(result.values, key)
			result.duplicates[key] = true
			continue
		}
		if result.nonStrings[key] {
			result.duplicates[key] = true
			continue
		}
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			result.nonStrings[key] = true
			continue
		}
		result.values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return parsedSourceLanguage{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return parsedSourceLanguage{}, err
	}
	return result, nil
}

func collectTargetLanguageKeys(data []byte) (map[string]bool, int, error) {
	if !utf8.Valid(data) {
		return nil, 0, errors.New("file is not UTF-8")
	}
	data = trimUTF8BOM(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, 0, err
	}
	if token != json.Delim('{') {
		return nil, 0, errors.New("root must be an object")
	}
	keys := make(map[string]bool)
	members := 0
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, 0, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, 0, errors.New("object key must be a string")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, 0, err
		}
		keys[key] = true
		members++
		if members > maxCatalogMembers {
			return nil, 0, fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, 0, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, 0, err
	}
	return keys, members, nil
}

func trimUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
}

func (a *languageAggregator) publish() error {
	namespaces := make([]string, 0, len(a.byNamespace))
	for namespace := range a.byNamespace {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		files := a.byNamespace[namespace]
		if len(files.sources) == 0 {
			continue
		}
		sort.Slice(files.sources, func(i, j int) bool { return files.sources[i].jar < files.sources[j].jar })
		for _, source := range files.sources {
			keys := make([]string, 0, len(source.duplicates))
			for key := range source.duplicates {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Printf("[WARN] %s/%s: duplicate source key %q skipped\n", filepath.Base(source.jar), namespace, key)
			}
			keys = make([]string, 0, len(source.nonStrings))
			for key := range source.nonStrings {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Printf("[WARN] %s/%s: non-string source key %q skipped\n", filepath.Base(source.jar), namespace, key)
			}
		}
		merged := make(map[string]languageValue)
		conflicts := make(map[string]languageConflict)
		for _, source := range files.sources {
			keys := make([]string, 0, len(source.values))
			for key := range source.values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				value := source.values[key]
				if existing, ok := merged[key]; ok {
					if existing.value != value {
						if _, reported := conflicts[key]; !reported {
							conflicts[key] = languageConflict{first: existing.source, second: source.jar}
						}
					}
					continue
				}
				merged[key] = languageValue{value: value, source: source.jar}
			}
		}
		conflictKeys := make([]string, 0, len(conflicts))
		for key := range conflicts {
			conflictKeys = append(conflictKeys, key)
		}
		sort.Strings(conflictKeys)
		for _, key := range conflictKeys {
			conflict := conflicts[key]
			fmt.Printf("[WARN] namespace %q translation key %q conflicts between JARs %q and %q; key skipped\n", namespace, key, filepath.Base(conflict.first), filepath.Base(conflict.second))
		}
		values := make(map[string]string, len(merged))
		for key, entry := range merged {
			_, conflict := conflicts[key]
			if !conflict && (a.refresh || !files.targets[key]) {
				values[key] = entry.value
			}
		}
		if len(values) == 0 {
			continue
		}
		data, err := json.MarshalIndent(values, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal pending language for namespace %q: %w", namespace, err)
		}
		data = append(data, '\n')
		if err := savePendingTranslationFile(a.outputPath, namespace, a.targetLocale, data); err != nil {
			return err
		}
		fmt.Printf("[PENDING] %s: assets/%s/lang/%s\n", namespace, namespace, pendingTranslationFileName(a.targetLocale))
	}
	return nil
}

func languageFile(path string) (namespace, language string, ok bool) {
	path, err := safeArchivePath(path)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "assets" || parts[1] == "" || parts[2] != "lang" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func sourceLanguageFileName() string {
	return sourceLanguageCode + ".json"
}

func targetLanguageFileName(targetLocale ...string) string {
	return selectedTargetLocale(targetLocale...) + ".json"
}

func pendingTranslationFileName(targetLocale ...string) string {
	return selectedTargetLocale(targetLocale...) + pendingFileSuffix + ".json"
}

func selectedTargetLocale(targetLocale ...string) string {
	if len(targetLocale) > 0 && targetLocale[0] != "" {
		return targetLocale[0]
	}
	return defaultTargetLanguageCode
}

func enabledDisabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func readLanguageFile(file *zip.File) ([]byte, error) {
	return readLimitedZipFile(file)
}

func savePendingTranslationFile(
	outputPath string,
	namespace string,
	targetLocale string,
	data []byte,
) error {
	destinationDir := filepath.Join(outputPath, "assets", namespace, "lang")
	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return fmt.Errorf("crear carpeta de fuentes pendientes: %w", err)
	}

	destinationPath := filepath.Join(destinationDir, pendingTranslationFileName(targetLocale))
	if err := os.WriteFile(destinationPath, data, 0644); err != nil {
		return fmt.Errorf("guardar fuente pendiente de %s: %w", namespace, err)
	}

	return nil
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
