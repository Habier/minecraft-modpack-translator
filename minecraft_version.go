package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type versionDetector struct {
	name  string
	paths func(string) []string
	parse func([]byte) (string, error)
}

var minecraftVersionDetectors = []versionDetector{
	{name: "mmc-pack.json", paths: metadataPaths("mmc-pack.json"), parse: parseMMCPackVersion},
	{name: "manifest.json", paths: metadataPaths("manifest.json"), parse: parseCurseManifestVersion},
}

func metadataPaths(name string) func(string) []string {
	return func(modpackPath string) []string {
		paths := []string{filepath.Join(modpackPath, name)}
		if filepath.Base(filepath.Clean(modpackPath)) == ".minecraft" {
			paths = append(paths, filepath.Join(filepath.Dir(filepath.Clean(modpackPath)), name))
		}
		return paths
	}
}

func detectMinecraftVersion(modpackPath string) (string, error) {
	for _, detector := range minecraftVersionDetectors {
		for _, path := range detector.paths(modpackPath) {
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("read Minecraft metadata %s: %w", path, err)
			}

			version, err := detector.parse(data)
			if err != nil {
				return "", fmt.Errorf("parse Minecraft metadata %s: %w", path, err)
			}
			return version, nil
		}
	}

	return "", fmt.Errorf("Minecraft version not found in %s or %s", filepath.Join(modpackPath, "mmc-pack.json"), filepath.Join(modpackPath, "manifest.json"))
}

func parseMMCPackVersion(data []byte) (string, error) {
	var pack struct {
		Components []struct {
			UID     string `json:"uid"`
			Version string `json:"version"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &pack); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	for _, component := range pack.Components {
		if component.UID == "net.minecraft" {
			version := strings.TrimSpace(component.Version)
			if version == "" {
				return "", errors.New(`component "net.minecraft" has no version`)
			}
			return version, nil
		}
	}
	return "", errors.New(`component "net.minecraft" not found`)
}

func parseCurseManifestVersion(data []byte) (string, error) {
	var manifest struct {
		Minecraft struct {
			Version string `json:"version"`
		} `json:"minecraft"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	version := strings.TrimSpace(manifest.Minecraft.Version)
	if version == "" {
		return "", errors.New("minecraft.version is missing or empty")
	}
	return version, nil
}

func resourcePackFormat(version string) (int, error) {
	format, ok := resourcePackFormats[version]
	if ok {
		return format, nil
	}
	if strings.HasPrefix(version, "1.21.9") || strings.HasPrefix(version, "1.21.10") || strings.HasPrefix(version, "1.21.11") || strings.HasPrefix(version, "26.") {
		return 0, fmt.Errorf("Minecraft %q is unsupported: it requires decimal resource pack formats and min_format/max_format metadata", version)
	}
	return 0, fmt.Errorf("Minecraft version %q is unsupported; only exact Java release versions 1.16.5 through 1.21.8 are supported", version)
}

var resourcePackFormats = map[string]int{
	"1.16.5": 6,
	"1.17":   7, "1.17.1": 7,
	"1.18": 8, "1.18.1": 8, "1.18.2": 8,
	"1.19": 9, "1.19.1": 9, "1.19.2": 9,
	"1.19.3": 12,
	"1.19.4": 13,
	"1.20":   15, "1.20.1": 15,
	"1.20.2": 18,
	"1.20.3": 22, "1.20.4": 22,
	"1.20.5": 32, "1.20.6": 32,
	"1.21": 34, "1.21.1": 34,
	"1.21.2": 42, "1.21.3": 42,
	"1.21.4": 46,
	"1.21.5": 55,
	"1.21.6": 63,
	"1.21.7": 64, "1.21.8": 64,
}
