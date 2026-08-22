package main

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"modpack-translator/internal/writeback"
)

func TestWritebackCreatesShareableZipWithKubeJSAndResourcePack(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, outputDirectory, "workspace")
	writeTestFiles(t, map[string][]byte{
		filepath.Join(workspace, "sources", "kubejs", "assets", "ftbquestlocalizer", "lang", "en_us.json"): []byte(`{"first":"First","second":"Second","third/key":"Third"}`),
		filepath.Join(workspace, "catalog", "catalog.v1.json"): []byte(`{
  "schema": "modpack-translator.catalog/v1",
	"source_locale": "en_us",
	"target_locale": "es_es",
	"entries": [
	    {"id":"std","source_kind":"standard_lang","source_file":"assets/example/lang/es_es.pending.json","locator":"/item.example.name","source":"Example","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
	    {"id":"third","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/third~1key","source":"Third","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
	    {"id":"first","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/first","source":"First","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}}
	  ]
}`),
		filepath.Join(workspace, "translations", "translations.v2.json"): []byte(`{
  "schema": "modpack-translator.translations/v2",
	  "target_locale": "es_es",
	  "prompt_version": "test",
	  "entries": [
	    {"id":"std","cache_key":"","source_sha256":"","token_signature":"","translation":"Ejemplo","translation_sha256":"","provider":"test","model":"test"},
	    {"id":"third","cache_key":"","source_sha256":"","token_signature":"","translation":"Tercero","translation_sha256":"","provider":"test","model":"test"},
	    {"id":"first","cache_key":"","source_sha256":"","token_signature":"","translation":"Primero","translation_sha256":"","provider":"test","model":"test"}
	  ]
}`),
	})

	if err := run(modpack); err != nil {
		t.Fatalf("run writeback: %v", err)
	}

	path := filepath.Join(modpack, outputDirectory, "export", "overrides", "kubejs", "assets", "ftbquestlocalizer", "lang", "es_es.json")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read KubeJS export: %v", err)
	}
	want := "{\n  \"first\": \"Primero\",\n  \"third/key\": \"Tercero\"\n}\n"
	if string(got) != want {
		t.Fatalf("KubeJS export = %q, want %q", got, want)
	}

	zipPath := writeback.ZipPath(modpack)
	entries := zipEntries(t, zipPath)
	for _, want := range []string{
		"kubejs/assets/ftbquestlocalizer/lang/es_es.json",
		"resourcepacks/ModpackTranslations/assets/example/lang/es_es.json",
	} {
		if !slices.Contains(entries, want) {
			t.Fatalf("ZIP entries = %#v, missing %q", entries, want)
		}
	}
	if !slices.IsSorted(entries) {
		t.Fatalf("ZIP entries are not sorted: %#v", entries)
	}
}

func TestWritebackDoesNotCreateZipWhenTranslationMissing(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, outputDirectory, "workspace")
	zipPath := writeback.ZipPath(modpack)
	writeTestFiles(t, map[string][]byte{
		filepath.Join(workspace, "catalog", "catalog.v1.json"): []byte(`{
  "schema": "modpack-translator.catalog/v1",
  "source_locale": "en_us",
  "target_locale": "es_es",
  "entries": [
    {"id":"translated","source_kind":"standard_lang","source_file":"assets/example/lang/es_es.pending.json","locator":"/translated","source":"Translated","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"missing","source_kind":"standard_lang","source_file":"assets/example/lang/es_es.pending.json","locator":"/missing","source":"Missing","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}}
  ]
}`),
		filepath.Join(workspace, "translations", "translations.v2.json"): []byte(`{
  "schema": "modpack-translator.translations/v2",
  "target_locale": "es_es",
  "prompt_version": "test",
  "entries": [
    {"id":"translated","cache_key":"","source_sha256":"","token_signature":"","translation":"Traducido","translation_sha256":"","provider":"test","model":"test"}
  ]
}`),
		zipPath: []byte("stale zip"),
	})

	err := run(modpack)
	if err == nil {
		t.Fatal("run succeeded with a missing translation")
	}
	if !strings.Contains(err.Error(), "translation coverage is incomplete") || !strings.Contains(err.Error(), "ZIP was not created") {
		t.Fatalf("error = %q, want incomplete coverage message", err)
	}
	if _, statErr := os.Stat(zipPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ZIP stat error = %v, want not exist", statErr)
	}
}

func TestWritebackDoesNotCreateZipWhenKubeJSSourceInvalid(t *testing.T) {
	tests := []struct {
		name        string
		sourceData  []byte
		wantMessage string
	}{
		{
			name:        "missing source",
			wantMessage: "reading KubeJS language source",
		},
		{
			name:        "invalid source JSON",
			sourceData:  []byte(`{"first":`),
			wantMessage: "reading KubeJS language key order",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modpack := t.TempDir()
			workspace := filepath.Join(modpack, outputDirectory, "workspace")
			sourcePath := filepath.Join(workspace, "sources", "kubejs", "assets", "ftbquestlocalizer", "lang", "en_us.json")
			files := map[string][]byte{
				filepath.Join(workspace, "catalog", "catalog.v1.json"): []byte(`{
  "schema": "modpack-translator.catalog/v1",
  "source_locale": "en_us",
  "target_locale": "es_es",
  "entries": [
    {"id":"first","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/first","source":"First","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}}
  ]
}`),
				filepath.Join(workspace, "translations", "translations.v2.json"): []byte(`{
  "schema": "modpack-translator.translations/v2",
  "target_locale": "es_es",
  "prompt_version": "test",
  "entries": [
    {"id":"first","cache_key":"","source_sha256":"","token_signature":"","translation":"Primero","translation_sha256":"","provider":"test","model":"test"}
  ]
}`),
				writeback.ZipPath(modpack): []byte("stale zip"),
			}
			if tt.sourceData != nil {
				files[sourcePath] = tt.sourceData
			}
			writeTestFiles(t, files)

			err := run(modpack)
			if err == nil {
				t.Fatal("run succeeded with invalid KubeJS source")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("error = %q, want %q", err, tt.wantMessage)
			}
			zipPath := writeback.ZipPath(modpack)
			if _, statErr := os.Stat(zipPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("ZIP stat error = %v, want not exist", statErr)
			}
		})
	}
}

func zipEntries(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open ZIP: %v", err)
	}
	defer reader.Close()
	entries := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		entries = append(entries, file.Name)
		contents, err := file.Open()
		if err != nil {
			t.Fatalf("open ZIP entry %s: %v", file.Name, err)
		}
		if _, err := io.Copy(io.Discard, contents); err != nil {
			contents.Close()
			t.Fatalf("read ZIP entry %s: %v", file.Name, err)
		}
		if err := contents.Close(); err != nil {
			t.Fatalf("close ZIP entry %s: %v", file.Name, err)
		}
	}
	return entries
}

func writeTestFiles(t *testing.T, files map[string][]byte) {
	t.Helper()
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("create directory for %s: %v", path, err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}
