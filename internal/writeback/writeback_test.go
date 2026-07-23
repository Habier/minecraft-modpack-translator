package writeback

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

func TestWorkspaceFTBWritebackTargetsOnlyCatalogedField(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/repeat.snbt"
	source := []byte(`{title:"Repeat",subtitle:"Repeat"}`)

	writeFTBWorkspace(t, workspace, sourceFile, source, []catalogEntryV1{
		ftbCatalogEntry("title", sourceFile, "/title", "Repeat", "Repetido", source, "object", nil),
	})

	_, err := Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace: %v", err)
	}
	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "config", "ftbquests", "quests", "quests", "repeat.snbt"),
		`{title:"Repetido",subtitle:"Repeat"}`,
	)
}

func TestWorkspaceFTBWritebackPreservesEscapedQuotesAndBackslashes(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/escaped.snbt"
	source := []byte(`{title:"A \"quote\" and C:\\path",subtitle:"Keep \\ slash"}`)

	writeFTBWorkspace(t, workspace, sourceFile, source, []catalogEntryV1{
		ftbCatalogEntry("title", sourceFile, "/title", `A "quote" and C:\path`, `Una "cita" y C:\ruta`, source, "object", nil),
	})

	_, err := Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace: %v", err)
	}
	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "config", "ftbquests", "quests", "quests", "escaped.snbt"),
		`{title:"Una \"cita\" y C:\\ruta",subtitle:"Keep \\ slash"}`,
	)
}

func TestWorkspaceFTBJSON5WritebackSkipsCommentsAndTargetsOnlyCatalogedField(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/commented.json5"
	source := []byte(`// file header
{
  // before title
  title: "Keep \"quoted\" value",
  /* between fields */
  subtitle: "Keep \"quoted\" value",
  nested: {
    // before nested text
    text: "Nested \"old\" value"
  }
}
`)
	entry := ftbCatalogEntry("nested_text", sourceFile, "/nested/text", `Nested "old" value`, `Nested "new" value`, source, "object", nil)
	entry.SourceKind = "ftbquests_json5"
	entry.Writeback.Format = "json5"

	writeFTBWorkspace(t, workspace, sourceFile, source, []catalogEntryV1{entry})

	_, err := Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace: %v", err)
	}
	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "config", "ftbquests", "quests", "quests", "commented.json5"),
		`// file header
{
  // before title
  title: "Keep \"quoted\" value",
  /* between fields */
  subtitle: "Keep \"quoted\" value",
  nested: {
    // before nested text
    text: "Nested \"new\" value"
  }
}
`,
	)
}

func TestFTBJSON5WritebackSkipsCommentsBetweenUnquotedKeyAndColon(t *testing.T) {
	tests := []struct {
		name   string
		source []byte
		want   string
	}{
		{
			name:   "block comment",
			source: []byte(`{title/*c*/:"Old",subtitle:"Old"}`),
			want:   `{title/*c*/:"New",subtitle:"Old"}`,
		},
		{
			name:   "line comment",
			source: []byte("{title//c\n:\"Old\",subtitle:\"Old\"}"),
			want:   "{title//c\n:\"New\",subtitle:\"Old\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/commented-key.json5"
			entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", tt.source, "object", nil)
			entry.SourceKind = "ftbquests_json5"
			entry.Writeback.Format = "json5"

			got, err := applyFTBWriteback(tt.source, ftbEntry{
				sourceFile: sourceFile,
				format:     entry.Writeback.Format,
				fields: map[string]fieldInfo{
					entry.Locator: {
						source:      entry.Source,
						translation: entry.Tokens[0].Text,
						valueType:   entry.Writeback.ValueType,
						container:   entry.Writeback.Container,
						arrayIndex:  entry.Writeback.ArrayIndex,
						sourceHash:  entry.Writeback.SourceSHA256,
					},
				},
			})
			if err != nil {
				t.Fatalf("apply FTB writeback: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("writeback output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFTBSNBTWritebackDoesNotAcceptCommentBetweenUnquotedKeyAndColon(t *testing.T) {
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/commented-key.snbt"
	source := []byte(`{title/*c*/:"Old"}`)
	entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", source, "object", nil)

	_, err := applyFTBWriteback(source, ftbEntry{
		sourceFile: sourceFile,
		format:     entry.Writeback.Format,
		fields: map[string]fieldInfo{
			entry.Locator: {
				source:      entry.Source,
				translation: entry.Tokens[0].Text,
				valueType:   entry.Writeback.ValueType,
				container:   entry.Writeback.Container,
				arrayIndex:  entry.Writeback.ArrayIndex,
				sourceHash:  entry.Writeback.SourceSHA256,
			},
		},
	})
	if err == nil {
		t.Fatal("FTB SNBT writeback accepted a comment between unquoted key and colon")
	}
}

func TestFTBSNBTWritebackDoesNotAcceptCommentsInSkippedValues(t *testing.T) {
	tests := []struct {
		name   string
		source []byte
	}{
		{
			name:   "line comment in skipped object",
			source: []byte("{other:{label://c\n\"Ignored\"},title:\"Old\"}"),
		},
		{
			name:   "block comment in skipped array",
			source: []byte(`{other:[/*c*/"Ignored"],title:"Old"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/skipped-comment.snbt"
			entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", tt.source, "object", nil)

			_, err := applyFTBWriteback(tt.source, ftbEntry{
				sourceFile: sourceFile,
				format:     entry.Writeback.Format,
				fields: map[string]fieldInfo{
					entry.Locator: {
						source:      entry.Source,
						translation: entry.Tokens[0].Text,
						valueType:   entry.Writeback.ValueType,
						container:   entry.Writeback.Container,
						arrayIndex:  entry.Writeback.ArrayIndex,
						sourceHash:  entry.Writeback.SourceSHA256,
					},
				},
			})
			if err == nil {
				t.Fatal("FTB SNBT writeback accepted a comment in a skipped value")
			}
			if !strings.Contains(err.Error(), "comments are not supported") {
				t.Fatalf("error = %q, want comments are not supported", err)
			}
		})
	}
}

func TestFTBJSON5WritebackAllowsCommentsInSkippedValues(t *testing.T) {
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/skipped-comment.json5"
	source := []byte("{other:{label://c\n\"Ignored\"},title:\"Old\"}")
	entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", source, "object", nil)
	entry.SourceKind = "ftbquests_json5"
	entry.Writeback.Format = "json5"

	got, err := applyFTBWriteback(source, ftbEntry{
		sourceFile: sourceFile,
		format:     entry.Writeback.Format,
		fields: map[string]fieldInfo{
			entry.Locator: {
				source:      entry.Source,
				translation: entry.Tokens[0].Text,
				valueType:   entry.Writeback.ValueType,
				container:   entry.Writeback.Container,
				arrayIndex:  entry.Writeback.ArrayIndex,
				sourceHash:  entry.Writeback.SourceSHA256,
			},
		},
	})
	if err != nil {
		t.Fatalf("apply FTB writeback: %v", err)
	}
	if string(got) != "{other:{label://c\n\"Ignored\"},title:\"New\"}" {
		t.Fatalf("writeback output = %q", got)
	}
}

func TestFTBJSON5WritebackRejectsUnterminatedBlockComment(t *testing.T) {
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/bad.json5"
	source := []byte(`{
  /* unfinished
  title: "Old"
}
`)
	entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", source, "object", nil)
	entry.SourceKind = "ftbquests_json5"
	entry.Writeback.Format = "json5"

	_, err := applyFTBWriteback(source, ftbEntry{
		sourceFile: sourceFile,
		format:     entry.Writeback.Format,
		fields: map[string]fieldInfo{
			entry.Locator: {
				source:      entry.Source,
				translation: entry.Tokens[0].Text,
				valueType:   entry.Writeback.ValueType,
				container:   entry.Writeback.Container,
				arrayIndex:  entry.Writeback.ArrayIndex,
				sourceHash:  entry.Writeback.SourceSHA256,
			},
		},
	})
	if err == nil {
		t.Fatal("FTB JSON5 writeback succeeded with an unterminated block comment")
	}
	if !strings.Contains(err.Error(), "unterminated block comment") {
		t.Fatalf("error = %q, want unterminated block comment", err)
	}
}

func TestWorkspaceFTBWritebackTargetsRecordedArrayIndex(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/description.snbt"
	source := []byte(`{description:["Same","Same","Same"]}`)
	index := 1

	writeFTBWorkspace(t, workspace, sourceFile, source, []catalogEntryV1{
		ftbCatalogEntry("description_1", sourceFile, "/description/1", "Same", "Solo este", source, "array", &index),
	})

	_, err := Workspace(modpack)
	if err != nil {
		t.Fatalf("writeback workspace: %v", err)
	}
	assertFileContent(t,
		filepath.Join(modpack, OutputDirectory, "export", "overrides", "config", "ftbquests", "quests", "quests", "description.snbt"),
		`{description:["Same","Solo este","Same"]}`,
	)
}

func TestFTBWritebackRejectsObjectContainerWithArrayIndex(t *testing.T) {
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/object-index.snbt"
	source := []byte(`{title:"Old"}`)
	index := 0
	entry := ftbCatalogEntry("title", sourceFile, "/title", "Old", "New", source, "object", &index)

	_, err := applyFTBWriteback(source, ftbEntry{
		sourceFile: sourceFile,
		format:     entry.Writeback.Format,
		fields: map[string]fieldInfo{
			entry.Locator: {
				source:      entry.Source,
				translation: entry.Tokens[0].Text,
				valueType:   entry.Writeback.ValueType,
				container:   entry.Writeback.Container,
				arrayIndex:  entry.Writeback.ArrayIndex,
				sourceHash:  entry.Writeback.SourceSHA256,
			},
		},
	})
	if err == nil {
		t.Fatal("FTB writeback accepted object metadata with an array index")
	}
	if !strings.Contains(err.Error(), "object metadata cannot include array index") {
		t.Fatalf("error = %q, want object metadata cannot include array index", err)
	}
}

func TestWorkspaceFTBWritebackRejectsStaleSourceHashWithoutZip(t *testing.T) {
	modpack := t.TempDir()
	workspace := filepath.Join(modpack, OutputDirectory, "workspace")
	zipPath := ZipPath(modpack)
	sourceFile := "sources/ftbquests/config/ftbquests/quests/quests/stale.snbt"
	source := []byte(`{title:"Fresh"}`)
	entry := ftbCatalogEntry("title", sourceFile, "/title", "Fresh", "Fresco", source, "object", nil)
	entry.Writeback.SourceSHA256 = strings.Repeat("0", 64)

	writeFTBWorkspace(t, workspace, sourceFile, source, []catalogEntryV1{entry})
	writeTestFiles(t, map[string][]byte{zipPath: []byte("stale zip")})

	_, err := Workspace(modpack)
	if err == nil {
		t.Fatal("writeback workspace succeeded with stale FTB source hash")
	}
	if !strings.Contains(err.Error(), "source hash mismatch") || !strings.Contains(err.Error(), sourceFile) {
		t.Fatalf("error = %q, want FTB source hash mismatch", err)
	}
	if _, statErr := os.Stat(zipPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ZIP stat error = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(modpack, OutputDirectory, "export", "overrides", "config", "ftbquests", "quests", "quests", "stale.snbt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("FTB export stat error = %v, want not exist", statErr)
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

func writeFTBWorkspace(t *testing.T, workspace, sourceFile string, source []byte, entries []catalogEntryV1) {
	t.Helper()
	files := map[string][]byte{
		filepath.Join(workspace, filepath.FromSlash(sourceFile)): source,
	}
	translations := make([]translationEntryV2, 0, len(entries))
	for _, entry := range entries {
		translations = append(translations, translationEntryV2{ID: entry.ID, Translation: entry.Tokens[0].Text})
		entry.Tokens = nil
	}
	catalog := catalogV1{Schema: "modpack-translator.catalog/v1", SourceLocale: SourceLang, TargetLocale: TargetLang, Entries: entries}
	catalogData, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	cache := translationCacheV2{Schema: "modpack-translator.translations/v2", TargetLocale: TargetLang, PromptVersion: "test", Entries: translations}
	cacheData, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		t.Fatalf("marshal translation cache: %v", err)
	}
	files[filepath.Join(workspace, "catalog", "catalog.v1.json")] = append(catalogData, '\n')
	files[filepath.Join(workspace, "translations", "translations.v2.json")] = append(cacheData, '\n')
	writeTestFiles(t, files)
}

func ftbCatalogEntry(id, sourceFile, locator, source, translation string, sourceData []byte, container string, arrayIndex *int) catalogEntryV1 {
	digest := sha256.Sum256(sourceData)
	return catalogEntryV1{
		ID:           id,
		SourceKind:   "ftbquests_snbt",
		SourceFile:   sourceFile,
		Locator:      locator,
		Source:       source,
		TargetLocale: TargetLang,
		Tokens:       []catalogTokenV1{{Text: translation}},
		Writeback: catalogWritebackV1{
			Format:       "snbt",
			ValueType:    "string",
			Container:    container,
			ArrayIndex:   arrayIndex,
			SourceSHA256: hex.EncodeToString(digest[:]),
			Encoding:     "UTF-8",
		},
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
