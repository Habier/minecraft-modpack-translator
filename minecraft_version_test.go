package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectMinecraftVersion(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		want      string
		wantError string
	}{
		{name: "MultiMC metadata", files: map[string]string{"mmc-pack.json": `{"components":[{"uid":"net.minecraft","version":"1.20.1"}]}`}, want: "1.20.1"},
		{name: "CurseForge metadata", files: map[string]string{"manifest.json": `{"minecraft":{"version":"1.19.4"}}`}, want: "1.19.4"},
		{name: "MultiMC takes precedence", files: map[string]string{
			"mmc-pack.json": `{"components":[{"uid":"net.minecraft","version":"1.21.4"}]}`,
			"manifest.json": `{"minecraft":{"version":"1.16.5"}}`,
		}, want: "1.21.4"},
		{name: "missing MultiMC falls back to CurseForge", files: map[string]string{"manifest.json": `{"minecraft":{"version":"1.18.2"}}`}, want: "1.18.2"},
		{name: "malformed recognized metadata stops fallback", files: map[string]string{
			"mmc-pack.json": `{not-json`,
			"manifest.json": `{"minecraft":{"version":"1.18.2"}}`,
		}, wantError: "mmc-pack.json"},
		{name: "missing metadata", wantError: "Minecraft version not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, contents := range tt.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
					t.Fatalf("write metadata: %v", err)
				}
			}
			got, err := detectMinecraftVersion(root)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantError)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("detectMinecraftVersion() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestDetectMinecraftVersionFromInstanceParent(t *testing.T) {
	instance := t.TempDir()
	gameDir := filepath.Join(instance, ".minecraft")
	if err := os.Mkdir(gameDir, 0755); err != nil {
		t.Fatalf("create game directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(instance, "mmc-pack.json"), []byte(`{"components":[{"uid":"net.minecraft","version":"1.21.8"}]}`), 0644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	got, err := detectMinecraftVersion(gameDir)
	if err != nil || got != "1.21.8" {
		t.Fatalf("detectMinecraftVersion() = %q, %v; want 1.21.8, nil", got, err)
	}
}

func TestResourcePackFormat(t *testing.T) {
	tests := []struct {
		version   string
		want      int
		wantError string
	}{
		{version: "1.16.5", want: 6},
		{version: "1.17", want: 7},
		{version: "1.19.2", want: 9},
		{version: "1.19.3", want: 12},
		{version: "1.20.4", want: 22},
		{version: "1.20.5", want: 32},
		{version: "1.21.8", want: 64},
		{version: "1.21.9", wantError: "min_format/max_format"},
		{version: "25w31a", wantError: "unsupported"},
		{version: "1.20.1-forge", wantError: "unsupported"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, err := resourcePackFormat(tt.version)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantError)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("resourcePackFormat(%q) = %d, %v; want %d, nil", tt.version, got, err, tt.want)
			}
		})
	}
}

func TestCreatePackMetadata(t *testing.T) {
	root := t.TempDir()
	_, output := outputPaths(root)
	if err := createPackMetadata(output, "1.21.4", 46); err != nil {
		t.Fatalf("createPackMetadata() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(output, "pack.mcmeta"))
	if err != nil {
		t.Fatalf("read pack.mcmeta: %v", err)
	}
	var got PackMeta
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse pack.mcmeta: %v", err)
	}
	if got.Pack.PackFormat != 46 || !strings.Contains(got.Pack.Description, "1.21.4") {
		t.Fatalf("pack.mcmeta = %#v, want format 46 and version in description", got)
	}
	workspace, _ := outputPaths(root)
	if _, err := os.Stat(filepath.Join(workspace, "pack.mcmeta")); !os.IsNotExist(err) {
		t.Fatalf("workspace pack.mcmeta should be absent, stat error = %v", err)
	}
}
