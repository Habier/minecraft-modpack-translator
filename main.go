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
	resourcePackName   = "ModpackTranslations"
	outputDirectory    = "modpack-translator-output"
	sourceLanguageCode = "en_us"
	targetLanguageCode = "es_es"
	pendingFileSuffix  = ".pending"
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

func runWithLanguageLimits(args []string, limits languageLimits) error {
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
	fmt.Printf("Minecraft detectado: %s (resource pack format %d)\n", minecraftVersion, packFormat)

	if err := prepareOutputs(workspacePath, exportPackPath, minecraftVersion, packFormat); err != nil {
		return err
	}

	jarFiles, err := filepath.Glob(filepath.Join(modsPath, "*.jar"))
	if err != nil {
		return err
	}

	fmt.Printf("Encontrados %d mods\n", len(jarFiles))
	extractor, err := newSourceExtractor(workspacePath)
	if err != nil {
		return err
	}
	defer extractor.abort()

	languages := newLanguageAggregatorWithLimits(extractor.stageWorkspace, limits)
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
	entryCount, _, err := buildCatalog(extractor.stageWorkspace)
	if err != nil {
		return fmt.Errorf("build translation catalog: %w", err)
	}
	if err := extractor.commitWorkspace(workspacePath); err != nil {
		return err
	}
	catalogPath := filepath.Join(workspacePath, "catalog", "catalog.v1.json")

	fmt.Printf("Fuentes estándar: procesados %d JARs\n", len(jarFiles))
	fmt.Printf("Fuentes Patchouli: %d archivos\n", extractor.counts.patchouli)
	fmt.Printf("Fuentes FTB Quests: %d archivos\n", extractor.counts.ftbquests)
	fmt.Printf("Fuentes KubeJS: %d archivos\n", extractor.counts.kubejs)
	fmt.Printf("Catálogo de traducción: %d entradas\n%s\n", entryCount, catalogPath)

	fmt.Printf("\nWorkspace de traducción (archivos pendientes):\n%s\n", workspacePath)
	fmt.Printf("\nResource pack de exportación (metadatos y traducciones terminadas solamente):\n%s\n", exportPackPath)
	if options.translate {
		translator, model, err := buildTranslatorChain(os.Getenv)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s\nTranslating catalog with the configured provider chain\n", providerChainSummary(os.Getenv))
		if err := translateWorkspace(context.Background(), workspacePath, model, translator, translationOptions{}); err != nil {
			return err
		}
		fmt.Printf("Validated translations cached at:\n%s\n", translationCachePath(workspacePath, model))
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

func prepareOutputs(workspacePath, exportPackPath, minecraftVersion string, packFormat int) error {
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return fmt.Errorf("crear workspace de traducción: %w", err)
	}
	if err := removePendingExportFiles(exportPackPath); err != nil {
		return err
	}
	return createPackMetadata(exportPackPath, minecraftVersion, packFormat)
}

func removePendingExportFiles(exportPackPath string) error {
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
		_, language, isLanguageFile := languageFile(relativePath)
		if isLanguageFile && language == pendingTranslationFileName() {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("eliminar archivo pendiente obsoleto del export: %w", err)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("limpiar archivos pendientes del export: %w", err)
	}
	return nil
}

func modpackPathFromArgs(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	currentDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("leer ruta actual: %w", err)
	}
	if hasModsDirectory(currentDirectory) {
		fmt.Printf("Modpack detectado: %s\n", currentDirectory)
		return currentDirectory, nil
	}

	candidates, err := findCommonModpacks()
	if err != nil {
		return "", err
	}

	if len(candidates) == 1 {
		fmt.Printf("Modpack detectado: %s\n", candidates[0])
		return candidates[0], nil
	}

	if len(candidates) == 0 {
		return "", errors.New("uso: minecraft-translator <ruta-del-modpack>\nNo se encontraron modpacks en la ruta actual ni en instalaciones comunes")
	}

	return selectModpack(candidates, os.Stdin, os.Stdout)
}

func selectModpack(candidates []string, input io.Reader, output io.Writer) (string, error) {
	fmt.Fprintln(output, "Se encontraron varios modpacks:")
	for i, candidate := range candidates {
		fmt.Fprintf(output, "%d. %s\n", i+1, candidate)
	}
	fmt.Fprint(output, "Elige un modpack por número: ")

	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("leer selección: %w", err)
		}
		return "", errors.New("no se recibió ninguna selección")
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(candidates) {
		return "", fmt.Errorf("selección inválida: elige un número entre 1 y %d", len(candidates))
	}

	selected := candidates[choice-1]
	fmt.Fprintf(output, "Modpack seleccionado: %s\n", selected)
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
			return nil, fmt.Errorf("leer instalación común %s: %w", root, err)
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

type namespaceLanguages struct {
	targets map[string]bool
	sources []languageSource
}

type languageSource struct {
	jar        string
	values     map[string]string
	duplicates map[string]bool
}

type parsedSourceLanguage struct {
	values     map[string]string
	duplicates map[string]bool
	members    int
}

type languageAggregator struct {
	outputPath  string
	byNamespace map[string]*namespaceLanguages
	budget      languageBudget
}

func newLanguageAggregator(outputPath string) *languageAggregator {
	return newLanguageAggregatorWithLimits(outputPath, defaultLanguageLimits())
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

func newLanguageAggregatorWithLimits(outputPath string, limits languageLimits) *languageAggregator {
	return &languageAggregator{
		outputPath:  outputPath,
		byNamespace: make(map[string]*namespaceLanguages),
		budget:      languageBudget{limits: limits},
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
		namespace, language, ok := languageFile(file.Name)
		if !ok {
			continue
		}

		files := byNamespace[namespace]
		if files == nil {
			files = &jarLanguages{}
			byNamespace[namespace] = files
		}

		if language != sourceLanguageFileName() && language != targetLanguageFileName() {
			continue
		}

		data, err := readLanguageFile(file)
		if err != nil {
			return fmt.Errorf("read %s in %s: %w", file.Name, filepath.Base(jarPath), err)
		}
		if err := a.budget.accountBytes(uint64(len(data)), jarPath, file.Name); err != nil {
			return err
		}
		if language == targetLanguageFileName() {
			keys, members, err := collectTargetLanguageKeys(data)
			if err != nil {
				return fmt.Errorf("parse %s in %s: %w", file.Name, filepath.Base(jarPath), err)
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
			fmt.Printf("[OK] %s/%s: %s existente\n", filepath.Base(jarPath), namespace, targetLanguageFileName())
		}
		for _, source := range files.sources {
			namespaceFiles.sources = append(namespaceFiles.sources, languageSource{jar: jarPath, values: source.values, duplicates: source.duplicates})
		}
		if len(files.targets) == 0 && len(files.sources) == 0 {
			fmt.Printf("[SKIP] %s/%s: sin %s ni %s\n", filepath.Base(jarPath), namespace, targetLanguageFileName(), sourceLanguageFileName())
		}
	}

	if len(byNamespace) == 0 {
		fmt.Printf("[SKIP] %s: sin archivos de idioma\n", filepath.Base(jarPath))
	}

	return nil
}

func parseStandardLanguage(data []byte) (parsedSourceLanguage, error) {
	if !utf8.Valid(data) {
		return parsedSourceLanguage{}, errors.New("file is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return parsedSourceLanguage{}, err
	}
	if token != json.Delim('{') {
		return parsedSourceLanguage{}, errors.New("root must be an object")
	}
	result := parsedSourceLanguage{values: make(map[string]string), duplicates: make(map[string]bool)}
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
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			return parsedSourceLanguage{}, fmt.Errorf("key %q must be a string", key)
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
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, 0, fmt.Errorf("key %q must be a string", key)
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
		duplicateJars := make(map[string][]string)
		for _, source := range files.sources {
			for key := range source.duplicates {
				if !files.targets[key] {
					duplicateJars[key] = append(duplicateJars[key], filepath.Base(source.jar))
				}
			}
		}
		if len(duplicateJars) > 0 {
			keys := make([]string, 0, len(duplicateJars))
			for key := range duplicateJars {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			key := keys[0]
			return fmt.Errorf("namespace %q duplicate source key %q in JAR(s) %q", namespace, key, strings.Join(duplicateJars[key], ", "))
		}
		merged := make(map[string]languageValue)
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
						return fmt.Errorf("namespace %q translation key %q conflicts between JARs %q and %q", namespace, key, filepath.Base(existing.source), filepath.Base(source.jar))
					}
					continue
				}
				merged[key] = languageValue{value: value, source: source.jar}
			}
		}
		values := make(map[string]string, len(merged))
		for key, entry := range merged {
			if !files.targets[key] {
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
		if err := savePendingTranslationFile(a.outputPath, namespace, data); err != nil {
			return err
		}
		fmt.Printf("[PENDING] %s: assets/%s/lang/%s\n", namespace, namespace, pendingTranslationFileName())
	}
	return nil
}

func languageFile(path string) (namespace, language string, ok bool) {
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	if len(parts) != 4 || parts[0] != "assets" || parts[1] == "" || parts[2] != "lang" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func sourceLanguageFileName() string {
	return sourceLanguageCode + ".json"
}

func targetLanguageFileName() string {
	return targetLanguageCode + ".json"
}

func pendingTranslationFileName() string {
	return targetLanguageCode + pendingFileSuffix + ".json"
}

func readLanguageFile(file *zip.File) ([]byte, error) {
	return readLimitedZipFile(file)
}

func savePendingTranslationFile(
	outputPath string,
	namespace string,
	data []byte,
) error {
	destinationDir := filepath.Join(outputPath, "assets", namespace, "lang")
	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return fmt.Errorf("crear carpeta de fuentes pendientes: %w", err)
	}

	destinationPath := filepath.Join(destinationDir, pendingTranslationFileName())
	if err := os.WriteFile(destinationPath, data, 0644); err != nil {
		return fmt.Errorf("guardar fuente pendiente de %s: %w", namespace, err)
	}

	return nil
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
