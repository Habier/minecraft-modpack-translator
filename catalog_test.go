package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogExtractsConservativeFieldsAndTokens(t *testing.T) {
	workspace := t.TempDir()
	writeFiles(t, "", map[string][]byte{
		filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName()):                      []byte(`{"empty":"","space":" \t","hello":"Hello %s","repeat":"Hello %s"}`),
		filepath.Join(workspace, "sources", "patchouli", "instance", "guide", "book.json"):                       []byte(`{"name":"Guide","landing_text":"Welcome","subtitle":"Sub","model":"technical"}`),
		filepath.Join(workspace, "sources", "patchouli", "instance", "guide", "en_us", "entries", "start.json"):  []byte(`{"name":"Start","icon":"mod:item","pages":["Short page",{"type":"patchouli:text","title":"Heading","text":"Body","recipe":"mod:recipe"},{"type":"patchouli:entity","name":"Creature","entity":"minecraft:pig"},{"type":"custom:page","text":"Do not infer"}]}`),
		filepath.Join(workspace, "sources", "patchouli", "instance", "guide", "en_us", "templates", "card.json"): []byte(`{"components":[{"type":"patchouli:text","text":"Literal","tooltip":["Tip one","Tip two"]},{"text":"#variable"}]}`),
		filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests", "quests", "abc.snbt"):  []byte(`{title:"Quest",subtitle:"Sub",description:["Line one","{@pagebreak}","Line two"],command:"/kill @a",id:"0123456789abcdef",tasks:[{title:"Task",type:"item",item:"minecraft:stone"}],rewards:[{title:"Reward",command:"say no"}]}`),
		filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests", "lang", "en_us.json5"): []byte(`{// comment
title: {'0123456789abcdef': 'Locale title',}, quest_desc: {'fedcba9876543210': ['One','Two']}, ignored: 'technical',}`),
	})

	count, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatalf("buildCatalog() error = %v", err)
	}
	if count == 0 || catalogPath != filepath.Join(workspace, "catalog", "catalog.v1.json") {
		t.Fatalf("buildCatalog() = %d, %q", count, catalogPath)
	}
	catalog := readCatalog(t, catalogPath)
	bySource := map[string]CatalogEntryV1{}
	helloOccurrences := 0
	for _, entry := range catalog.Entries {
		bySource[entry.Source] = entry
		if entry.Source == "Hello %s" {
			helloOccurrences++
		}
		for _, forbidden := range []string{"technical", "mod:recipe", "minecraft:pig", "Do not infer", "/kill @a", "0123456789abcdef", "say no", "{@pagebreak}"} {
			if entry.Source == forbidden {
				t.Errorf("catalog contains excluded value %q", forbidden)
			}
		}
	}
	for _, expected := range []string{"Hello %s", "Guide", "Short page", "Heading", "Body", "Creature", "Literal", "Tip one", "Quest", "Task", "Reward", "Locale title", "One", "Two"} {
		if _, ok := bySource[expected]; !ok {
			t.Errorf("catalog is missing %q", expected)
		}
	}
	if _, ok := bySource[" \t"]; ok {
		t.Error("Unicode-whitespace-only source was not skipped")
	}
	if len(bySource["Hello %s"].Tokens) != 1 || bySource["Hello %s"].Tokens[0].Start != 6 {
		t.Errorf("placeholder tokens = %#v", bySource["Hello %s"].Tokens)
	}
	if helloOccurrences != 2 {
		t.Errorf("repeated source occurrences = %d, want 2", helloOccurrences)
	}
}

func TestPatchouliI18NModeExcludesLocalizationKeys(t *testing.T) {
	workspace := t.TempDir()
	writeFiles(t, "", map[string][]byte{
		filepath.Join(workspace, "sources", "patchouli", "jars", "mod-123", "data", "example", "patchouli_books", "guide", "book.json"):                        []byte(`{"i18n":true,"name":"book.example.name"}`),
		filepath.Join(workspace, "sources", "patchouli", "jars", "mod-123", "assets", "example", "patchouli_books", "guide", "en_us", "entries", "start.json"): []byte(`{"name":"entry.example.name","pages":["entry.example.page"]}`),
	})
	count, _, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("i18n catalog entries = %d, want 0", count)
	}
}

func TestCatalogExtractsKubeJSLangEntries(t *testing.T) {
	workspace := t.TempDir()
	sourceFile := filepath.Join(workspace, "sources", "kubejs", "assets", "ftbquestlocalizer", "lang", "en_us.json")
	writeFiles(t, "", map[string][]byte{sourceFile: []byte(`{"empty":"","space":" \t","quest/title":"Quest %s","nested":{"ignored":"no"}}`)})

	count, catalogPath, err := buildCatalog(workspace)
	if err == nil {
		t.Fatal("buildCatalog() error = nil, want non-string value error")
	}
	if count != 0 || catalogPath != "" {
		t.Fatalf("failed build = %d, %q", count, catalogPath)
	}

	if err := os.WriteFile(sourceFile, []byte(`{"empty":"","space":" \t","quest/title":"Quest %s"}`), 0644); err != nil {
		t.Fatal(err)
	}
	count, catalogPath, err = buildCatalog(workspace)
	if err != nil {
		t.Fatalf("buildCatalog() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("KubeJS catalog count = %d, want 1", count)
	}
	entry := readCatalog(t, catalogPath).Entries[0]
	if entry.SourceKind != "kubejs_lang" || entry.SourceFile != "sources/kubejs/assets/ftbquestlocalizer/lang/en_us.json" || entry.Locator != "/quest~1title" {
		t.Fatalf("KubeJS entry identity = %#v", entry)
	}
	if entry.Source != "Quest %s" || entry.Writeback.Format != "json" || entry.Writeback.ValueType != "string" || entry.Writeback.Container != "object" {
		t.Fatalf("KubeJS entry content/writeback = %#v", entry)
	}
	if len(entry.Tokens) != 1 {
		t.Fatalf("KubeJS tokens = %#v", entry.Tokens)
	}
}

func TestCatalogExtractsFTBChapterImageHoverLists(t *testing.T) {
	tests := []struct {
		name        string
		snbt        string
		wantSources []string
		wantCount   int
	}{
		{
			name:        "empty hover list",
			snbt:        `{title:"Chapter",subtitle:[],images:[{hover:[]}]}`,
			wantSources: []string{"Chapter"},
			wantCount:   1,
		},
		{
			name:        "non-empty hover list",
			snbt:        `{title:"Chapter",subtitle:[],images:[{hover:["First line","Second line"]}]}`,
			wantSources: []string{"Chapter", "First line", "Second line"},
			wantCount:   3,
		},
		{
			name:        "scalar hover",
			snbt:        `{title:"Chapter",subtitle:[],images:[{hover:"Single line"}]}`,
			wantSources: []string{"Chapter", "Single line"},
			wantCount:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeFiles(t, "", map[string][]byte{
				filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests", "chapters", "chapter.snbt"): []byte(tt.snbt),
			})

			count, catalogPath, err := buildCatalog(workspace)
			if err != nil {
				t.Fatalf("buildCatalog() error = %v", err)
			}
			if count != tt.wantCount {
				t.Fatalf("catalog count = %d, want %d", count, tt.wantCount)
			}
			bySource := map[string]CatalogEntryV1{}
			for _, entry := range readCatalog(t, catalogPath).Entries {
				bySource[entry.Source] = entry
			}
			for _, source := range tt.wantSources {
				if _, ok := bySource[source]; !ok {
					t.Errorf("catalog is missing %q", source)
				}
			}
			if entry, ok := bySource["First line"]; ok && (entry.Locator != "/images/0/hover/0" || entry.Writeback.Container != "array" || entry.Writeback.ArrayIndex == nil || *entry.Writeback.ArrayIndex != 0) {
				t.Fatalf("first hover entry = %#v", entry)
			}
		})
	}
}

func TestCatalogStableIDIgnoresSourceAndTracksFileHash(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	writeFiles(t, "", map[string][]byte{file: []byte(`{"key":"First"}`)})
	_, path, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	first := readCatalog(t, path).Entries[0]
	if err := os.WriteFile(file, []byte(`{"key":"Second"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildCatalog(workspace); err != nil {
		t.Fatal(err)
	}
	second := readCatalog(t, path).Entries[0]
	if first.ID != second.ID {
		t.Errorf("stable ID changed: %s != %s", first.ID, second.ID)
	}
	if first.Writeback.SourceSHA256 == second.Writeback.SourceSHA256 {
		t.Error("source file hash did not change")
	}
}

func TestCatalogRejectsMalformedInputsAndPreservesPreviousCatalog(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	writeFiles(t, "", map[string][]byte{file: []byte(`{"key":"valid"}`)})
	_, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	previous := string(mustRead(t, catalogPath))

	tests := []struct {
		name string
		data []byte
	}{
		{name: "duplicate", data: []byte(`{"key":"one","key":"two"}`)},
		{name: "non string", data: []byte(`{"key":42}`)},
		{name: "nested", data: []byte(`{"key":{"nested":"no"}}`)},
		{name: "invalid UTF-8", data: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(file, tt.data, 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := buildCatalog(workspace); err == nil {
				t.Fatal("buildCatalog() error = nil")
			}
			if got := string(mustRead(t, catalogPath)); got != previous {
				t.Error("failed build changed previous catalog")
			}
		})
	}
}

func TestCatalogSuccessfulRerunRemovesStaleEntriesAndExcludesExport(t *testing.T) {
	root := t.TempDir()
	workspace, export := outputPaths(root)
	file := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	writeFiles(t, "", map[string][]byte{file: []byte(`{"old":"stale","keep":"current"}`)})
	if _, _, err := buildCatalog(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"keep":"current"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, catalogPath, err := buildCatalog(workspace); err != nil {
		t.Fatal(err)
	} else {
		catalog := readCatalog(t, catalogPath)
		if len(catalog.Entries) != 1 || catalog.Entries[0].Source != "current" {
			t.Fatalf("entries = %#v", catalog.Entries)
		}
	}
	assertAbsent(t, filepath.Join(export, "catalog"))
}

func TestJSON5DuplicateAndLimits(t *testing.T) {
	if err := checkJSON5Structure([]byte(`{title:{'0123456789abcdef':'one','0123456789abcdef':'two'}}`)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
	deep := strings.Repeat("[", maxCatalogDepth+1) + "0" + strings.Repeat("]", maxCatalogDepth+1)
	if err := checkJSON5Structure([]byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("depth error = %v", err)
	}
}

func TestCatalogLocatorOrderingUsesNumericArraySegments(t *testing.T) {
	entries := []CatalogEntryV1{{SourceKind: "patchouli", SourceFile: "a", Locator: "/pages/10"}, {SourceKind: "patchouli", SourceFile: "a", Locator: "/pages/2"}}
	if !catalogEntryLess(entries[1], entries[0]) {
		t.Error("numeric locator ordering places 10 before 2")
	}
}

func readCatalog(t *testing.T, filePath string) CatalogV1 {
	t.Helper()
	var catalog CatalogV1
	if err := json.Unmarshal(mustRead(t, filePath), &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}
