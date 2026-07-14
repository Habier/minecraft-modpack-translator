package main

import (
	"archive/zip"
	"bufio"
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
)

const (
	packFormat         = 34
	resourcePackName   = "ModpackTranslations"
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
	modpackPath, err := modpackPathFromArgs(os.Args[1:])
	if err != nil {
		exitError(err)
	}

	modsPath := filepath.Join(modpackPath, "mods")
	outputPath := filepath.Join(
		modpackPath,
		"resourcepacks",
		resourcePackName,
	)

	if err := createPackMetadata(outputPath); err != nil {
		exitError(err)
	}

	jarFiles, err := filepath.Glob(filepath.Join(modsPath, "*.jar"))
	if err != nil {
		exitError(err)
	}

	fmt.Printf("Encontrados %d mods\n", len(jarFiles))

	for _, jarPath := range jarFiles {
		if err := processMod(jarPath, outputPath); err != nil {
			fmt.Printf("[ERROR] %s: %v\n", filepath.Base(jarPath), err)
		}
	}

	fmt.Printf("\nResource pack generado en:\n%s\n", outputPath)
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

func createPackMetadata(outputPath string) error {
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return fmt.Errorf("crear carpeta del resource pack: %w", err)
	}

	meta := PackMeta{
		Pack: PackInfo{
			PackFormat:  packFormat,
			Description: "Modpack translation workspace",
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

func processMod(jarPath, outputPath string) error {
	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return fmt.Errorf("abrir JAR: %w", err)
	}
	defer reader.Close()

	type languages struct {
		source []byte
		target []byte
	}

	byNamespace := make(map[string]*languages)

	for _, file := range reader.File {
		namespace, language, ok := languageFile(file.Name)
		if !ok {
			continue
		}

		files := byNamespace[namespace]
		if files == nil {
			files = &languages{}
			byNamespace[namespace] = files
		}

		if language != sourceLanguageFileName() && language != targetLanguageFileName() {
			continue
		}

		data, err := readLanguageFile(file)
		if err != nil {
			return fmt.Errorf("leer %s: %w", file.Name, err)
		}
		if language == targetLanguageFileName() {
			files.target = data
		} else {
			files.source = data
		}
	}

	for namespace, files := range byNamespace {
		if files.target != nil {
			fmt.Printf("[OK] %s/%s: %s existente\n", filepath.Base(jarPath), namespace, targetLanguageFileName())
			continue
		}

		if files.source != nil {
			if err := savePendingTranslationFile(outputPath, namespace, files.source); err != nil {
				return err
			}
			fmt.Printf("[PENDING] %s/%s: assets/%s/lang/%s\n", filepath.Base(jarPath), namespace, namespace, pendingTranslationFileName())
			continue
		}

		if files.target == nil && files.source == nil {
			fmt.Printf("[SKIP] %s/%s: sin %s ni %s\n", filepath.Base(jarPath), namespace, targetLanguageFileName(), sourceLanguageFileName())
		}
	}

	if len(byNamespace) == 0 {
		fmt.Printf("[SKIP] %s: sin archivos de idioma\n", filepath.Base(jarPath))
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
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
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
