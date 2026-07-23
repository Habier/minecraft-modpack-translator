package writeback

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWorkspaceWritesTranslatedOverridesAndDeterministicZip(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")

	writeTestFiles(t, map[string][]byte{
		filepath.Join(workspace, "sources", "kubejs", "assets", "ftbquestlocalizer", "lang", "en_us.json"): []byte("{\n  \"second\": \"Second\",\n  \"first\": \"First\"\n}\n"),
		filepath.Join(workspace, "sources", "patchouli", "generated", "assets", "example", "patchouli_books", "guide", "en_us", "entries", "intro.json"): []byte(`{
  "name": "Intro",
  "pages": [
    {"type": "patchouli:text", "text": "Welcome"},
    {"type": "patchouli:text", "text": "Goodbye"}
  ]
}`),
		filepath.Join(workspace, "catalog", "catalog.v1.json"): []byte(`{
  "schema": "modpack-translator.catalog/v1",
  "source_locale": "en_us",
  "target_locale": "es_es",
  "entries": [
    {"id":"std_b","source_kind":"standard_lang","source_file":"assets/example/lang/es_es.pending.json","locator":"/item.example.b","source":"B","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"std_a","source_kind":"standard_lang","source_file":"assets/example/lang/es_es.pending.json","locator":"/item.example.a","source":"A","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"kube_extra_b","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/extra.b","source":"Extra B","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"kube_first","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/first","source":"First","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"kube_extra_a","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/extra.a","source":"Extra A","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"kube_second","source_kind":"kubejs_lang","source_file":"sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json","locator":"/second","source":"Second","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"patchouli_name","source_kind":"patchouli","source_file":"sources/patchouli/generated/assets/example/patchouli_books/guide/en_us/entries/intro.json","locator":"/name","source":"Intro","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}},
    {"id":"patchouli_page","source_kind":"patchouli","source_file":"sources/patchouli/generated/assets/example/patchouli_books/guide/en_us/entries/intro.json","locator":"/pages/1/text","source":"Goodbye","target_locale":"es_es","tokens":[],"writeback":{"format":"json","value_type":"string","container":"object","source_file_sha256":"","encoding":"UTF-8"}}
  ]
}`),
		filepath.Join(workspace, "translations", "translations.v2.json"): []byte(`{
  "schema": "modpack-translator.translations/v2",
  "target_locale": "es_es",
  "prompt_version": "test",
  "entries": [
    {"id":"std_b","translation":"Be"},
    {"id":"std_a","translation":"A"},
    {"id":"kube_extra_b","translation":"Extra B ES"},
    {"id":"kube_first","translation":"Primero"},
    {"id":"kube_extra_a","translation":"Extra A ES"},
    {"id":"kube_second","translation":"Segundo"},
    {"id":"patchouli_name","translation":"Introduccion"},
    {"id":"patchouli_page","translation":"Adios"}
  ]
}`),
	})

	zipPath, err := Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace: %v", err)
	}
	firstZip := readFile(t, zipPath)

	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "resourcepacks", ResourcePackName, "assets", "example", "lang", "es_es.json"),
		"{\n  \"item.example.a\": \"A\",\n  \"item.example.b\": \"Be\"\n}\n",
	)
	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "kubejs", "assets", "ftbquestlocalizer", "lang", "es_es.json"),
		"{\n  \"second\": \"Segundo\",\n  \"first\": \"Primero\",\n  \"extra.a\": \"Extra A ES\",\n  \"extra.b\": \"Extra B ES\"\n}\n",
	)

	patchouliPath := filepath.Join(modpack, OutputDirectory, "export", "overrides", "resourcepacks", ResourcePackName, "assets", "example", "patchouli_books", "guide", "es_es", "entries", "intro.json")
	patchouli := decodeJSONObject(t, patchouliPath)
	if got := patchouli["name"]; got != "Introduccion" {
		t.Fatalf("Patchouli name = %#v, want Introduccion", got)
	}
	pages := patchouli["pages"].([]any)
	if got := pages[0].(map[string]any)["text"]; got != "Welcome" {
		t.Fatalf("Patchouli page 0 text = %#v, want Welcome", got)
	}
	if got := pages[1].(map[string]any)["text"]; got != "Adios" {
		t.Fatalf("Patchouli page 1 text = %#v, want Adios", got)
	}

	wantEntries := []string{
		"kubejs/assets/ftbquestlocalizer/lang/es_es.json",
		"resourcepacks/ModpackTranslations/assets/example/lang/es_es.json",
		"resourcepacks/ModpackTranslations/assets/example/patchouli_books/guide/es_es/entries/intro.json",
	}
	gotEntries := zipEntries(t, zipPath)
	if !slices.Equal(gotEntries, wantEntries) {
		t.Fatalf("ZIP entries = %#v, want %#v", gotEntries, wantEntries)
	}

	zipPath, err = Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace second run: %v", err)
	}
	if secondZip := readFile(t, zipPath); !bytes.Equal(secondZip, firstZip) {
		t.Fatal("ZIP bytes changed between identical writeback runs")
	}
}

func TestWorkspaceAbortsAndRemovesStaleZipWhenTranslationsIncomplete(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	zipPath := ZipPath(modpack)

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
    {"id":"translated","translation":"Traducido"}
  ]
}`),
		zipPath: []byte("stale zip"),
	})

	_, err := Workspace(modpack)
	if err == nil {
		t.Fatal("writeback workspace succeeded with incomplete translations")
	}
	if !strings.Contains(err.Error(), "translation coverage is incomplete") || !strings.Contains(err.Error(), "ZIP was not created") {
		t.Fatalf("error = %q, want incomplete coverage message", err)
	}
	if _, statErr := os.Stat(zipPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ZIP stat error = %v, want not exist", statErr)
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
		if file.Modified.UTC() != zipModTime {
			t.Fatalf("ZIP entry %s modtime = %s, want %s", file.Name, file.Modified.UTC(), zipModTime)
		}
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
	if !slices.IsSorted(entries) {
		t.Fatalf("ZIP entries are not sorted: %#v", entries)
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

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got := readFile(t, path)
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func decodeJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(readFile(t, path), &got); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return got
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
