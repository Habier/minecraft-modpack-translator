package main

import (
	"bufio"
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

	"modpack-translator/internal/provider"
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
		translator, _, err := provider.BuildChain(os.Getenv, appendTranslationLog)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s\nTranslating catalog with the configured provider chain\n", provider.ChainSummary(os.Getenv))
		if err := translateWorkspace(context.Background(), workspacePath, translator, translationOptions{}); err != nil {
			return err
		}
		fmt.Printf("Validated translations cached at:\n%s\n", translationCachePath(workspacePath, targetLocale))
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

func enabledDisabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
