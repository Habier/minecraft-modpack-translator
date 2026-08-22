package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchouliJarExtraction(t *testing.T) {
	root := t.TempDir()
	jarPath := filepath.Join(root, "Example Mod.jar")
	entries := []zipEntry{
		{"assets/example/patchouli_books/guide/en_us/categories/start.json", []byte("category\x00bytes")},
		{"assets/example/patchouli_books/guide/en_us/entries/nested/page.json", []byte("entry")},
		{"assets/example/patchouli_books/guide/en_us/templates/card.json", []byte("template")},
		{"data/example/patchouli_books/guide/book.json", []byte("declaration")},
		{"assets/example/patchouli_books/guide/es_es/entries/no.json", []byte("locale")},
		{"assets/example/patchouli_books/guide/en_us/images/no.png", []byte("texture")},
		{"assets/example/patchouli_books/guide/en_us/entries/no.txt", []byte("text")},
		{"assets/example/textures/no.png", []byte("unrelated")},
	}
	writeTestJar(t, jarPath, entries)
	before := mustRead(t, jarPath)

	workspace := filepath.Join(root, "workspace")
	extractor := mustExtractor(t, workspace)
	if err := extractor.extractPatchouliJar(jarPath); err != nil {
		t.Fatalf("extractPatchouliJar() error = %v", err)
	}
	if extractor.counts.patchouli != 4 {
		t.Fatalf("Patchouli count = %d, want 4", extractor.counts.patchouli)
	}
	sourceDirs, err := os.ReadDir(filepath.Join(extractor.stageRoot, "patchouli", "jars"))
	if err != nil || len(sourceDirs) != 1 {
		t.Fatalf("source directories = %v, %v", sourceDirs, err)
	}
	if !strings.HasPrefix(sourceDirs[0].Name(), "example-mod-") || len(sourceDirs[0].Name()) != len("example-mod-")+12 {
		t.Fatalf("stable source ID = %q", sourceDirs[0].Name())
	}
	for _, entry := range entries[:4] {
		got := mustRead(t, filepath.Join(extractor.stageRoot, "patchouli", "jars", sourceDirs[0].Name(), filepath.FromSlash(entry.name)))
		if !bytes.Equal(got, entry.data) {
			t.Errorf("%s bytes = %q, want %q", entry.name, got, entry.data)
		}
	}
	for _, entry := range entries[4:] {
		assertAbsent(t, filepath.Join(extractor.stageRoot, "patchouli", "jars", sourceDirs[0].Name(), filepath.FromSlash(entry.name)))
	}
	if after := mustRead(t, jarPath); !bytes.Equal(before, after) {
		t.Fatal("source JAR was modified")
	}
}

func TestPatchouliJarStableSourceIDAvoidsSameBasenameCollision(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one", "mod.jar")
	second := filepath.Join(root, "two", "mod.jar")
	for _, directory := range []string{filepath.Dir(first), filepath.Dir(second)} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestJar(t, first, []zipEntry{{"assets/a/patchouli_books/book/en_us/entries/page.json", []byte("one")}})
	writeTestJar(t, second, []zipEntry{{"assets/a/patchouli_books/book/en_us/entries/page.json", []byte("two")}})
	extractor := mustExtractor(t, filepath.Join(root, "workspace"))
	if err := extractor.extractPatchouliJar(first); err != nil {
		t.Fatal(err)
	}
	if err := extractor.extractPatchouliJar(second); err != nil {
		t.Fatal(err)
	}
	directories, err := os.ReadDir(filepath.Join(extractor.stageRoot, "patchouli", "jars"))
	if err != nil || len(directories) != 2 || directories[0].Name() == directories[1].Name() {
		t.Fatalf("source directories = %v, %v", directories, err)
	}
}

func TestPatchouliJarRejectsDifferingBytesAtSameDestination(t *testing.T) {
	root := t.TempDir()
	jarPath := filepath.Join(root, "duplicate.jar")
	entry := "assets/a/patchouli_books/book/en_us/entries/page.json"
	writeTestJar(t, jarPath, []zipEntry{{entry, []byte("first")}, {entry, []byte("second")}})
	extractor := mustExtractor(t, filepath.Join(root, "workspace"))
	if err := extractor.extractPatchouliJar(jarPath); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("extractPatchouliJar() error = %v, want collision error", err)
	}
}

func TestPatchouliJarRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name      string
		entryName string
		symlink   bool
	}{
		{name: "parent traversal", entryName: "../escape.json"},
		{name: "absolute path", entryName: "/escape.json"},
		{name: "drive qualified", entryName: "C:/escape.json"},
		{name: "drive relative", entryName: "C:escape.json"},
		{name: "backslash traversal", entryName: `..\escape.json`},
		{name: "ZIP symlink", entryName: "assets/a/patchouli_books/b/en_us/entries/link.json", symlink: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			jarPath := filepath.Join(root, "bad.jar")
			writeSpecialJar(t, jarPath, tt.entryName, tt.symlink)
			extractor := mustExtractor(t, filepath.Join(root, "workspace"))
			if err := extractor.extractPatchouliJar(jarPath); err == nil {
				t.Fatal("extractPatchouliJar() error = nil, want unsafe-entry error")
			}
		})
	}
}

func TestExternalPatchouliCopiesOnlySupportedStructureWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"patchouli_books/guide/book.json":                      []byte("book"),
		"patchouli_books/guide/en_us/categories/start.json":    []byte("category"),
		"patchouli_books/guide/en_us/entries/nested/page.json": []byte("entry"),
		"patchouli_books/guide/en_us/templates/card.json":      []byte("template"),
		"patchouli_books/guide/es_es/entries/no.json":          []byte("locale"),
		"patchouli_books/guide/en_us/entries/no.txt":           []byte("text"),
		"patchouli_books/guide/en_us/images/no.json":           []byte("wrong subtree"),
	}
	writeFiles(t, root, files)
	linkedTarget := filepath.Join(root, "linked.json")
	if err := os.WriteFile(linkedTarget, []byte("linked"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "patchouli_books", "guide", "en_us", "entries", "link.json")
	symlinkCreated := os.Symlink(linkedTarget, link) == nil

	extractor := mustExtractor(t, filepath.Join(root, "workspace"))
	if err := extractor.extractInstancePatchouli(root); err != nil {
		t.Fatalf("extractInstancePatchouli() error = %v", err)
	}
	for relative, want := range files {
		destination := filepath.Join(extractor.stageRoot, "patchouli", "instance", strings.TrimPrefix(relative, "patchouli_books/"))
		allowed := strings.HasSuffix(relative, "/book.json") || strings.Contains(relative, "/en_us/categories/") || strings.Contains(relative, "/en_us/entries/") && strings.HasSuffix(relative, ".json") || strings.Contains(relative, "/en_us/templates/")
		if allowed {
			if got := mustRead(t, destination); !bytes.Equal(got, want) {
				t.Errorf("%s bytes = %q, want %q", relative, got, want)
			}
		} else {
			assertAbsent(t, destination)
		}
	}
	if symlinkCreated {
		assertAbsent(t, filepath.Join(extractor.stageRoot, "patchouli", "instance", "guide", "en_us", "entries", "link.json"))
	}
}

func TestFTBQuestsExtraction(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string][]byte
		wantRoot  string
		wantFiles []string
		wantCount int
	}{
		{
			name: "SNBT config",
			files: map[string][]byte{
				"config/ftbquests/quests/data.snbt":                          []byte("data"),
				"config/ftbquests/quests/chapters/start.snbt":                []byte("chapter"),
				"config/ftbquests/quests/lang/id_id/chapters/malformed.snbt": []byte(`{title:"Skystrike"quest:{}}`),
				"config/ftbquests/quests/settings.toml":                      []byte("settings"),
				"world/ftbquests/progress.snbt":                              []byte("progress"),
				"world/serverconfig/ftbquests-server.snbt":                   []byte("server"),
				"saves/save/ftbquests/progress.snbt":                         []byte("save"),
			},
			wantRoot: "config/ftbquests/quests", wantFiles: []string{"data.snbt", "chapters/start.snbt"}, wantCount: 2,
		},
		{
			name:     "JSON5 config",
			files:    map[string][]byte{"config/ftbquests/quests/data.json5": []byte("{}"), "config/ftbquests/quests/chapters/a.json5": []byte("{}")},
			wantRoot: "config/ftbquests/quests", wantFiles: []string{"data.json5", "chapters/a.json5"}, wantCount: 2,
		},
		{
			name:     "defaultconfigs fallback",
			files:    map[string][]byte{"defaultconfigs/ftbquests/quests/data.snbt": []byte("default"), "defaultconfigs/ftbquests/quests/reward_tables/a.snbt": []byte("reward"), "defaultconfigs/ftbquests/quests/lang/id_id/chapters/malformed.snbt": []byte(`{title:"Skystrike"quest:{}}`)},
			wantRoot: "defaultconfigs/ftbquests/quests", wantFiles: []string{"data.snbt", "reward_tables/a.snbt"}, wantCount: 2,
		},
		{
			name:     "config precedence",
			files:    map[string][]byte{"config/ftbquests/quests/data.snbt": []byte("config"), "defaultconfigs/ftbquests/quests/data.snbt": []byte("default")},
			wantRoot: "config/ftbquests/quests", wantFiles: []string{"data.snbt"}, wantCount: 1,
		},
		{
			name:      "missing sentinel",
			files:     map[string][]byte{"config/ftbquests/quests/chapters/a.snbt": []byte("chapter"), "defaultconfigs/ftbquests/quests/chapters/b.json5": []byte("chapter")},
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, tt.files)
			extractor := mustExtractor(t, filepath.Join(root, "workspace"))
			if err := extractor.extractFTBQuests(root); err != nil {
				t.Fatalf("extractFTBQuests() error = %v", err)
			}
			if extractor.counts.ftbquests != tt.wantCount {
				t.Fatalf("FTB count = %d, want %d", extractor.counts.ftbquests, tt.wantCount)
			}
			for _, relative := range tt.wantFiles {
				want := tt.files[filepath.ToSlash(filepath.Join(tt.wantRoot, relative))]
				got := mustRead(t, filepath.Join(extractor.stageRoot, "ftbquests", tt.wantRoot, relative))
				if !bytes.Equal(got, want) {
					t.Errorf("%s bytes = %q, want %q", relative, got, want)
				}
			}
			assertAbsent(t, filepath.Join(extractor.stageRoot, "ftbquests", "world"))
			assertAbsent(t, filepath.Join(extractor.stageRoot, "ftbquests", "saves"))
			if tt.wantRoot != "" {
				assertAbsent(t, filepath.Join(extractor.stageRoot, "ftbquests", tt.wantRoot, "lang"))
			}
		})
	}
}

func TestKubeJSLangExtractionRefreshExistingTarget(t *testing.T) {
	tests := []struct {
		name      string
		refresh   bool
		wantCount int
		wantSkip  bool
	}{
		{name: "skip existing target", wantCount: 1, wantSkip: true},
		{name: "refresh includes existing target", refresh: true, wantCount: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string][]byte{
				"kubejs/assets/ftbquestlocalizer/lang/en_us.json": []byte(`{"title":"Quest"}`),
				"kubejs/assets/skipme/lang/en_us.json":            []byte(`{"title":"Already translated"}`),
				"kubejs/assets/skipme/lang/es_es.json":            []byte(`{"title":"Ya traducido"}`),
				"kubejs/assets/notlang/en_us.json":                []byte(`{"title":"Wrong shape"}`),
				"kubejs/data/example/lang/en_us.json":             []byte(`{"title":"Wrong root"}`),
			}
			writeFiles(t, root, files)

			extractor := mustExtractor(t, filepath.Join(root, "workspace"))
			extractor.refresh = tt.refresh
			if err := extractor.extractKubeJSLang(root); err != nil {
				t.Fatalf("extractKubeJSLang() error = %v", err)
			}
			if extractor.counts.kubejs != tt.wantCount {
				t.Fatalf("KubeJS count = %d, want %d", extractor.counts.kubejs, tt.wantCount)
			}
			got := mustRead(t, filepath.Join(extractor.stageRoot, "kubejs", "assets", "ftbquestlocalizer", "lang", "en_us.json"))
			if string(got) != string(files["kubejs/assets/ftbquestlocalizer/lang/en_us.json"]) {
				t.Fatalf("staged KubeJS bytes = %q", got)
			}
			skipPath := filepath.Join(extractor.stageRoot, "kubejs", "assets", "skipme", "lang", "en_us.json")
			if tt.wantSkip {
				assertAbsent(t, skipPath)
			} else if string(mustRead(t, skipPath)) != string(files["kubejs/assets/skipme/lang/en_us.json"]) {
				t.Fatalf("refreshed KubeJS bytes mismatch")
			}
			assertAbsent(t, filepath.Join(extractor.stageRoot, "kubejs", "assets", "notlang", "en_us.json"))
		})
	}
}

func TestSourceCommitRemovesStaleFilesAndPreservesWorkspaceAndExport(t *testing.T) {
	root := t.TempDir()
	workspace, export := outputPaths(root)
	stale := filepath.Join(workspace, "sources", "patchouli", "stale.json")
	standard := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	exported := filepath.Join(export, "keep.txt")
	writeFiles(t, "", map[string][]byte{stale: []byte("stale"), standard: []byte("language"), exported: []byte("export")})

	extractor := mustExtractor(t, workspace)
	if err := extractor.write(filepath.Join("patchouli", "instance", "guide", "book.json"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := extractor.commit(workspace); err != nil {
		t.Fatalf("commit() error = %v", err)
	}
	assertAbsent(t, stale)
	if string(mustRead(t, standard)) != "language" || string(mustRead(t, exported)) != "export" {
		t.Fatal("source commit changed standard workspace or export files")
	}
}

func TestExtractionFailurePreservesPreviousCompleteSourceTree(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	previous := filepath.Join(workspace, "sources", "ftbquests", "previous.snbt")
	writeFiles(t, "", map[string][]byte{previous: []byte("previous")})
	jarPath := filepath.Join(root, "bad.jar")
	writeSpecialJar(t, jarPath, "../escape.json", false)

	extractor := mustExtractor(t, workspace)
	if err := extractor.write(filepath.Join("patchouli", "new.json"), []byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	if err := extractor.extractPatchouliJar(jarPath); err == nil {
		t.Fatal("extractPatchouliJar() error = nil, want error")
	}
	extractor.abort()
	if string(mustRead(t, previous)) != "previous" {
		t.Fatal("previous source tree changed after extraction failure")
	}
	assertAbsent(t, filepath.Join(workspace, "sources", "patchouli", "new.json"))
}

func mustExtractor(t *testing.T, workspace string) *sourceExtractor {
	t.Helper()
	extractor, err := newSourceExtractor(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(extractor.abort)
	return extractor
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s should be absent, stat error = %v", path, err)
	}
}

func writeFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for relative, data := range files {
		filePath := relative
		if root != "" {
			filePath = filepath.Join(root, filepath.FromSlash(relative))
		}
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSpecialJar(t *testing.T, jarPath, name string, symlink bool) {
	t.Helper()
	file, err := os.Create(jarPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	if symlink {
		header.SetMode(os.ModeSymlink | 0777)
	}
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
