package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxPendingSourceFileSize = 16 << 20
	maxPendingSourceTotal    = 512 << 20
	maxPendingSourceFiles    = 20000
)

type sourceCounts struct {
	patchouli int
	ftbquests int
	kubejs    int
}

type sourceExtractor struct {
	stageWorkspace string
	stageRoot      string
	files          int
	bytes          int64
	counts         sourceCounts
	refresh        bool
	targetLocale   string
}

func newSourceExtractor(workspacePath string, targetLocale ...string) (*sourceExtractor, error) {
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return nil, fmt.Errorf("create source workspace: %w", err)
	}
	stageWorkspace, err := os.MkdirTemp(workspacePath, ".workspace-staging-")
	if err != nil {
		return nil, fmt.Errorf("create source staging directory: %w", err)
	}
	stageRoot := filepath.Join(stageWorkspace, "sources")
	for _, directory := range []string{stageRoot, filepath.Join(stageWorkspace, "assets")} {
		if err := os.Mkdir(directory, 0755); err != nil {
			os.RemoveAll(stageWorkspace)
			return nil, fmt.Errorf("create staged workspace: %w", err)
		}
	}
	return &sourceExtractor{stageWorkspace: stageWorkspace, stageRoot: stageRoot, targetLocale: selectedTargetLocale(targetLocale...)}, nil
}

func (e *sourceExtractor) abort() {
	_ = os.RemoveAll(e.stageWorkspace)
}

func (e *sourceExtractor) commit(workspacePath string) error {
	destination := filepath.Join(workspacePath, "sources")
	backup := filepath.Join(workspacePath, ".sources-backup")
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove old source backup: %w", err)
	}
	hadPrevious := false
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("preserve previous source tree: %w", err)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect previous source tree: %w", err)
	}
	if err := os.Rename(e.stageRoot, destination); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("publish source tree: %w", err)
	}
	if hadPrevious {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove source backup after publish: %w", err)
		}
	}
	return nil
}

func (e *sourceExtractor) commitWorkspace(workspacePath string) error {
	components := []string{"assets", "sources", "catalog"}
	backedUp, published := []string{}, []string{}
	rollback := func() {
		for _, name := range published {
			_ = os.RemoveAll(filepath.Join(workspacePath, name))
		}
		for _, name := range backedUp {
			_ = os.Rename(filepath.Join(workspacePath, "."+name+"-backup"), filepath.Join(workspacePath, name))
		}
	}
	for _, name := range components {
		destination, backup := filepath.Join(workspacePath, name), filepath.Join(workspacePath, "."+name+"-backup")
		if err := os.RemoveAll(backup); err != nil {
			rollback()
			return fmt.Errorf("remove old %s backup: %w", name, err)
		}
		if _, err := os.Lstat(destination); err == nil {
			if err := os.Rename(destination, backup); err != nil {
				rollback()
				return fmt.Errorf("preserve previous %s: %w", name, err)
			}
			backedUp = append(backedUp, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			rollback()
			return fmt.Errorf("inspect previous %s: %w", name, err)
		}
		if err := os.Rename(filepath.Join(e.stageWorkspace, name), destination); err != nil {
			rollback()
			return fmt.Errorf("publish %s: %w", name, err)
		}
		published = append(published, name)
	}
	for _, name := range backedUp {
		if err := os.RemoveAll(filepath.Join(workspacePath, "."+name+"-backup")); err != nil {
			return fmt.Errorf("remove %s backup: %w", name, err)
		}
	}
	return os.RemoveAll(e.stageWorkspace)
}

func (e *sourceExtractor) extractPatchouliJar(jarPath string) error {
	jar, err := os.Open(jarPath)
	if err != nil {
		return fmt.Errorf("open JAR for source identity: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, jar); err != nil {
		jar.Close()
		return fmt.Errorf("hash JAR for source identity: %w", err)
	}
	if err := jar.Close(); err != nil {
		return fmt.Errorf("close JAR after source identity: %w", err)
	}
	sourceID := sanitizedSourceName(filepath.Base(jarPath)) + "-" + hex.EncodeToString(hash.Sum(nil)[:6])

	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return fmt.Errorf("open Patchouli JAR: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		clean, err := safeArchivePath(file.Name)
		if err != nil {
			return fmt.Errorf("unsafe JAR entry %q: %w", file.Name, err)
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe JAR entry %q: symbolic links are not allowed", file.Name)
		}
		if file.FileInfo().IsDir() || !patchouliJarPath(clean) {
			continue
		}
		if !file.Mode().IsRegular() {
			return fmt.Errorf("unsafe Patchouli entry %q: not a regular file", file.Name)
		}
		data, err := readLimitedZipFile(file)
		if err != nil {
			return fmt.Errorf("read Patchouli entry %q: %w", file.Name, err)
		}
		destination := filepath.Join("patchouli", "jars", sourceID, filepath.FromSlash(clean))
		if err := e.write(destination, data); err != nil {
			return fmt.Errorf("extract Patchouli entry %q: %w", file.Name, err)
		}
		e.counts.patchouli++
	}
	return nil
}

func patchouliJarPath(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) == 5 && parts[0] == "data" && parts[1] != "" && parts[2] == "patchouli_books" && parts[3] != "" && parts[4] == "book.json" {
		return true
	}
	if len(parts) < 7 || parts[0] != "assets" || parts[1] == "" || parts[2] != "patchouli_books" || parts[3] == "" || parts[4] != "en_us" {
		return false
	}
	switch parts[5] {
	case "categories", "entries", "templates":
		return strings.HasSuffix(parts[len(parts)-1], ".json")
	default:
		return false
	}
}

func (e *sourceExtractor) extractInstancePatchouli(modpackPath string) error {
	root := filepath.Join(modpackPath, "patchouli_books")
	return e.walkRegularFiles(root, func(relative string) bool {
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "book.json" {
			return true
		}
		if len(parts) < 4 || parts[0] == "" || parts[1] != "en_us" {
			return false
		}
		switch parts[2] {
		case "categories", "entries", "templates":
			return strings.HasSuffix(parts[len(parts)-1], ".json")
		default:
			return false
		}
	}, func(relative string, data []byte) error {
		if err := e.write(filepath.Join("patchouli", "instance", relative), data); err != nil {
			return err
		}
		e.counts.patchouli++
		return nil
	})
}

func (e *sourceExtractor) extractFTBQuests(modpackPath string) error {
	var root, relativeRoot string
	for _, candidate := range []string{filepath.Join("config", "ftbquests", "quests"), filepath.Join("defaultconfigs", "ftbquests", "quests")} {
		candidateRoot := filepath.Join(modpackPath, candidate)
		if regularFile(filepath.Join(candidateRoot, "data.snbt")) || regularFile(filepath.Join(candidateRoot, "data.json5")) {
			root, relativeRoot = candidateRoot, candidate
			break
		}
	}
	if root == "" {
		return nil
	}
	return e.walkRegularFiles(root, func(relative string) bool {
		extension := strings.ToLower(filepath.Ext(relative))
		return extension == ".snbt" || extension == ".json5"
	}, func(relative string, data []byte) error {
		if err := e.write(filepath.Join("ftbquests", relativeRoot, relative), data); err != nil {
			return err
		}
		e.counts.ftbquests++
		return nil
	})
}

func (e *sourceExtractor) extractKubeJSLang(modpackPath string) error {
	root := filepath.Join(modpackPath, "kubejs", "assets")
	return e.walkRegularFiles(root, func(relative string) bool {
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] != "lang" || parts[2] != sourceLanguageCode+".json" {
			return false
		}
		return e.refresh || !regularFile(filepath.Join(root, parts[0], "lang", targetLanguageFileName(e.targetLocale)))
	}, func(relative string, data []byte) error {
		if err := e.write(filepath.Join("kubejs", "assets", relative), data); err != nil {
			return err
		}
		e.counts.kubejs++
		return nil
	})
}

func (e *sourceExtractor) walkRegularFiles(root string, selectFile func(string) bool, copyFile func(string, []byte) error) error {
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect source root %s: %w", root, err)
	}
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk source %s: %w", filePath, walkErr)
		}
		if filePath == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect source %s: %w", filePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(filePath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		if !selectFile(relative) {
			return nil
		}
		if info.Size() > maxPendingSourceFileSize {
			return fmt.Errorf("source file %s is %d bytes; limit is %d", filePath, info.Size(), maxPendingSourceFileSize)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read source %s: %w", filePath, err)
		}
		return copyFile(relative, data)
	})
}

func (e *sourceExtractor) write(relative string, data []byte) error {
	if len(data) > maxPendingSourceFileSize {
		return fmt.Errorf("source file %s is %d bytes; limit is %d", relative, len(data), maxPendingSourceFileSize)
	}
	destination := filepath.Join(e.stageRoot, relative)
	if existing, err := os.ReadFile(destination); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("destination collision at %s with different bytes", relative)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination %s: %w", relative, err)
	}
	if e.files+1 > maxPendingSourceFiles {
		return fmt.Errorf("pending source file count exceeds limit %d at %s", maxPendingSourceFiles, relative)
	}
	if e.bytes+int64(len(data)) > maxPendingSourceTotal {
		return fmt.Errorf("pending source bytes exceed limit %d at %s", maxPendingSourceTotal, relative)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return fmt.Errorf("create destination for %s: %w", relative, err)
	}
	if err := os.WriteFile(destination, data, 0644); err != nil {
		return fmt.Errorf("write destination %s: %w", relative, err)
	}
	e.files++
	e.bytes += int64(len(data))
	return nil
}

func readLimitedZipFile(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxPendingSourceFileSize {
		return nil, fmt.Errorf("uncompressed size %d exceeds limit %d", file.UncompressedSize64, maxPendingSourceFileSize)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxPendingSourceFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPendingSourceFileSize {
		return nil, fmt.Errorf("content exceeds limit %d", maxPendingSourceFileSize)
	}
	return data, nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || hasWindowsDrivePrefix(name) {
		return "", errors.New("absolute, drive-qualified, and backslash paths are not allowed")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != name && clean+"/" != name {
		return "", errors.New("non-canonical or parent path is not allowed")
	}
	return strings.TrimSuffix(clean, "/"), nil
}

func hasWindowsDrivePrefix(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	drive := name[0]
	return drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z'
}

func sanitizedSourceName(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	var result strings.Builder
	for _, char := range strings.ToLower(name) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			result.WriteRune(char)
		} else {
			result.WriteByte('-')
		}
	}
	clean := strings.Trim(result.String(), "-._")
	if clean == "" {
		return "jar"
	}
	return clean
}

func regularFile(filePath string) bool {
	info, err := os.Lstat(filePath)
	return err == nil && info.Mode().IsRegular() && !isReparsePoint(filePath)
}
