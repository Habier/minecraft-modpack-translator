package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

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
			keys, err := collectTargetLanguageKeys(data)
			if err != nil {
				if !a.force {
					return fmt.Errorf("parse %s in %s: %w", file.Name, filepath.Base(jarPath), err)
				}
				fmt.Printf("[WARN] %s/%s: ignoring malformed target %s: %v\n", filepath.Base(jarPath), namespace, file.Name, err)
				continue
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

func readLanguageFile(file *zip.File) ([]byte, error) {
	return readLimitedZipFile(file)
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
	files uint64
	bytes uint64
}

type languageBudget struct {
	limits languageLimits
	files  uint64
	bytes  uint64
}

func defaultLanguageLimits() languageLimits {
	return languageLimits{files: maxPendingSourceFiles, bytes: maxPendingSourceTotal}
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

func processMod(jarPath, outputPath string) error {
	aggregator := newLanguageAggregator(outputPath)
	if err := aggregator.addJar(jarPath); err != nil {
		return err
	}
	return aggregator.publish()
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

func collectTargetLanguageKeys(data []byte) (map[string]bool, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("file is not UTF-8")
	}
	data = trimUTF8BOM(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, errors.New("root must be an object")
	}
	keys := make(map[string]bool)
	members := 0
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("object key must be a string")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		keys[key] = true
		members++
		if members > maxCatalogMembers {
			return nil, fmt.Errorf("object exceeds %d members", maxCatalogMembers)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return keys, nil
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
