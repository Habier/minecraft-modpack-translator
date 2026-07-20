package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	data []byte
}

func TestProcessModSelectsLanguagePerNamespace(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		ok      []string
		pending map[string][]byte
		absent  []string
		output  string
	}{
		{
			name: "preserves Spanish byte for byte",
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", []byte("{\n  \"key\": \"Espa\xc3\xb1ol\"\n}\n")},
			},
			ok:     []string{"example"},
			absent: []string{"example"},
			output: "[OK] mod.jar/example: es_es.json existente",
		},
		{
			name: "writes English to pending translation source",
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", []byte("{\"key\":\"English\"}")},
			},
			pending: map[string][]byte{"example": []byte("{\n  \"key\": \"English\"\n}\n")},
			absent:  []string{"example"},
			output:  "[PENDING] example: assets/example/lang/es_es.pending.json",
		},
		{
			name: "same JAR full Spanish target emits no pending when English appears last",
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", []byte(`{"key":"Spanish"}`)},
				{"assets/example/lang/en_us.json", []byte(`{"key":"English"}`)},
			},
			ok:     []string{"example"},
			absent: []string{"example"},
			output: "[OK] mod.jar/example: es_es.json existente",
		},
		{
			name: "same JAR full Spanish target emits no pending when English appears first",
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", []byte(`{"key":"English"}`)},
				{"assets/example/lang/es_es.json", []byte(`{"key":"Spanish"}`)},
			},
			ok:     []string{"example"},
			absent: []string{"example"},
			output: "[OK] mod.jar/example: es_es.json existente",
		},
		{
			name: "same JAR partial Spanish target emits missing English keys",
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", []byte(`{"translated":"Spanish"}`)},
				{"assets/example/lang/en_us.json", []byte(`{"translated":"English","missing":"Pending"}`)},
			},
			ok:      []string{"example"},
			pending: map[string][]byte{"example": []byte("{\n  \"missing\": \"Pending\"\n}\n")},
			output:  "[OK] mod.jar/example: es_es.json existente",
		},
		{
			name: "skips namespace without English or Spanish",
			entries: []zipEntry{
				{"assets/example/lang/fr_fr.json", []byte("french")},
			},
			absent: []string{"example"},
			output: "[SKIP] mod.jar/example: sin es_es.json ni en_us.json",
		},
		{
			name: "handles multiple namespaces independently",
			entries: []zipEntry{
				{"assets/alpha/lang/en_us.json", []byte(`{"key":"Alpha English"}`)},
				{"assets/beta/lang/en_us.json", []byte(`{"key":"Beta English"}`)},
				{"assets/beta/lang/es_es.json", []byte(`{"key":"Beta Spanish"}`)},
				{"assets/gamma/lang/de_de.json", []byte("gamma german")},
			},
			ok: []string{"beta"},
			pending: map[string][]byte{
				"alpha": []byte("{\n  \"key\": \"Alpha English\"\n}\n"),
			},
			absent: []string{"beta", "gamma"},
			output: "[SKIP] mod.jar/gamma: sin es_es.json ni en_us.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			jarPath := filepath.Join(root, "mod.jar")
			writeTestJar(t, jarPath, tt.entries)
			outputPath := filepath.Join(root, "pack")
			jarBefore, err := os.ReadFile(jarPath)
			if err != nil {
				t.Fatalf("read source JAR before processing: %v", err)
			}

			output, err := captureStdout(t, func() error {
				return processMod(jarPath, outputPath)
			})
			if err != nil {
				t.Fatalf("processMod() error = %v", err)
			}
			jarAfter, err := os.ReadFile(jarPath)
			if err != nil {
				t.Fatalf("read source JAR after processing: %v", err)
			}
			if !bytes.Equal(jarAfter, jarBefore) {
				t.Error("processMod() modified the source JAR")
			}
			if tt.output != "" && !strings.Contains(output, tt.output) {
				t.Errorf("output = %q, want it to contain %q", output, tt.output)
			}

			for _, namespace := range tt.ok {
				wantOutput := "[OK] mod.jar/" + namespace + ": es_es.json existente"
				if !strings.Contains(output, wantOutput) {
					t.Errorf("output = %q, want it to contain %q", output, wantOutput)
				}

				if _, pending := tt.pending[namespace]; !pending {
					pendingPath := filepath.Join(outputPath, "assets", namespace, "lang", pendingTranslationFileName())
					if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
						t.Errorf("expected %s to be absent, stat error = %v", pendingPath, err)
					}
				}
			}

			for _, namespace := range tt.absent {
				path := filepath.Join(outputPath, "assets", namespace, "lang", targetLanguageFileName())
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("expected %s to be absent, stat error = %v", path, err)
				}
			}

			for namespace, want := range tt.pending {
				path := filepath.Join(outputPath, "assets", namespace, "lang", pendingTranslationFileName())
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read pending translation source: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("pending bytes = %q, want %q", got, want)
				}
			}

		})
	}
}

func TestRunCreatesWorkspaceAndExportWithoutPendingExportFiles(t *testing.T) {
	modpackPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(modpackPath, "mods"), 0755); err != nil {
		t.Fatalf("create mods directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modpackPath, "manifest.json"), []byte(`{"minecraft":{"version":"1.20.1"}}`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	jarPath := filepath.Join(modpackPath, "mods", "mod.jar")
	writeTestJar(t, jarPath, []zipEntry{
		{"assets/example/lang/en_us.json", []byte(`{"key":"English"}`)},
		{"assets/example/patchouli_books/guide/en_us/entries/start.json", []byte(`{"name":"Start"}`)},
	})

	workspacePath, exportPackPath := outputPaths(modpackPath)
	stalePending := filepath.Join(exportPackPath, "assets", "stale", "lang", pendingTranslationFileName())
	if err := os.MkdirAll(filepath.Dir(stalePending), 0755); err != nil {
		t.Fatalf("create stale export directory: %v", err)
	}
	if err := os.WriteFile(stalePending, []byte("stale"), 0644); err != nil {
		t.Fatalf("write stale pending export: %v", err)
	}
	userFile := filepath.Join(exportPackPath, "notes.txt")
	if err := os.WriteFile(userFile, []byte("keep"), 0644); err != nil {
		t.Fatalf("write unrelated export file: %v", err)
	}

	output, err := captureStdout(t, func() error { return run([]string{modpackPath}) })
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	pendingPath := filepath.Join(workspacePath, "assets", "example", "lang", pendingTranslationFileName())
	if _, err := os.Stat(pendingPath); err != nil {
		t.Fatalf("workspace pending file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "pack.mcmeta")); !os.IsNotExist(err) {
		t.Errorf("workspace pack.mcmeta should be absent, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(exportPackPath, "pack.mcmeta")); err != nil {
		t.Fatalf("export pack.mcmeta: %v", err)
	}
	patchouliMatches, err := filepath.Glob(filepath.Join(workspacePath, "sources", "patchouli", "jars", "*", "assets", "example", "patchouli_books", "guide", "en_us", "entries", "start.json"))
	if err != nil || len(patchouliMatches) != 1 {
		t.Fatalf("workspace Patchouli files = %v, %v", patchouliMatches, err)
	}
	if _, err := os.Stat(filepath.Join(exportPackPath, "sources")); !os.IsNotExist(err) {
		t.Fatalf("export sources directory should be absent, stat error = %v", err)
	}
	if _, err := os.Stat(stalePending); !os.IsNotExist(err) {
		t.Errorf("stale export pending file should be absent, stat error = %v", err)
	}
	if data, err := os.ReadFile(userFile); err != nil || string(data) != "keep" {
		t.Errorf("unrelated export file = %q, %v; want preserved", data, err)
	}
	for _, want := range []string{workspacePath, exportPackPath, "archivos pendientes", "traducciones terminadas solamente", "Fuentes estándar:", "Fuentes Patchouli: 1 archivos", "Fuentes FTB Quests: 0 archivos"} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	}
}

func TestRunReplacesOwnedWorkspaceAsOneSnapshot(t *testing.T) {
	modpack, jar := testModpack(t)
	english := []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"English"}`)}}
	writeTestJar(t, jar, english)
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	workspace, _ := outputPaths(modpack)
	pending := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	os.Remove(jar)
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, pending)
	writeTestJar(t, jar, english)
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	writeTestJar(t, jar, append(english, zipEntry{"assets/example/lang/es_es.json", []byte(`{"key":"Español"}`)}))
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, pending)
}

func TestRunMergesStandardLanguagesByKey(t *testing.T) {
	for _, tt := range []struct {
		name, first, second string
		want                string
	}{
		{name: "disjoint keys", first: `{"alpha":"A"}`, second: `{"beta":"B"}`, want: "{\n  \"alpha\": \"A\",\n  \"beta\": \"B\"\n}\n"},
		{name: "same key and value", first: `{"key":"same"}`, second: `{"key":"same"}`, want: "{\n  \"key\": \"same\"\n}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			modpack, _ := testModpack(t)
			firstJar := filepath.Join(modpack, "mods", "a.jar")
			secondJar := filepath.Join(modpack, "mods", "b.jar")
			writeTestJar(t, firstJar, []zipEntry{{"assets/example/lang/en_us.json", []byte(tt.first)}})
			writeTestJar(t, secondJar, []zipEntry{{"assets/example/lang/en_us.json", []byte(tt.second)}})
			firstBefore, secondBefore := mustRead(t, firstJar), mustRead(t, secondJar)
			if err := run([]string{modpack}); err != nil {
				t.Fatal(err)
			}
			workspace, _ := outputPaths(modpack)
			if got := string(mustRead(t, filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName()))); got != tt.want {
				t.Fatalf("pending = %q, want %q", got, tt.want)
			}
			catalog := readCatalog(t, filepath.Join(workspace, "catalog", "catalog.v1.json"))
			if len(catalog.Entries) != len(strings.Split(strings.TrimSpace(tt.want), "\n"))-2 {
				t.Fatalf("catalog entries = %d", len(catalog.Entries))
			}
			digest := sha256.Sum256([]byte(tt.want))
			for _, entry := range catalog.Entries {
				if entry.Writeback.SourceSHA256 != hex.EncodeToString(digest[:]) {
					t.Fatalf("catalog hash = %s, want merged pending hash", entry.Writeback.SourceSHA256)
				}
			}
			if !bytes.Equal(firstBefore, mustRead(t, firstJar)) || !bytes.Equal(secondBefore, mustRead(t, secondJar)) {
				t.Fatal("source JAR changed")
			}
		})
	}
}

func TestRunStandardLanguageFailuresPreserveWorkspace(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source []byte
		want   string
	}{
		{"conflicting value", []byte(`{"key":"incoming"}`), `translation key "key" conflicts between JARs "a.jar" and "b.jar"`},
		{"malformed JSON", []byte(`{"key":`), "EOF"},
		{"invalid UTF-8", []byte{'{', '"', 'k', '"', ':', '"', 0xff, '"', '}'}, "file is not UTF-8"},
		{"non-string value", []byte(`{"key":7}`), `key "key" must be a string`},
		{"duplicate key", []byte(`{"key":"one","key":"two"}`), `namespace "example" duplicate source key "key" in JAR(s) "b.jar"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			modpack, jar := testModpack(t)
			writeTestJar(t, jar, []zipEntry{{"assets/previous/lang/en_us.json", []byte(`{"old":"complete"}`)}})
			if err := run([]string{modpack}); err != nil {
				t.Fatal(err)
			}
			workspace, _ := outputPaths(modpack)
			before := workspaceSnapshot(t, workspace)
			os.Remove(jar)
			writeTestJar(t, filepath.Join(modpack, "mods", "a.jar"), []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"existing"}`)}})
			writeTestJar(t, filepath.Join(modpack, "mods", "b.jar"), []zipEntry{{"assets/example/lang/en_us.json", tt.source}})
			err := run([]string{modpack})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() error = %v, want %q", err, tt.want)
			}
			if tt.name == "duplicate key" && (strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "two")) {
				t.Fatalf("duplicate error leaked source values: %v", err)
			}
			if got := workspaceSnapshot(t, workspace); got != before {
				t.Fatal("failed extraction changed live workspace")
			}
		})
	}
}

func TestRunSubtractsTargetKeysAcrossJarsRegardlessOfOrder(t *testing.T) {
	for _, tt := range []struct {
		name, targetName, target, source, want string
	}{
		{"partial target first", "a.jar", `{"translated":"Ya"}`, `{"translated":"Done","missing":"English"}`, "{\n  \"missing\": \"English\"\n}\n"},
		{"partial target last", "z.jar", `{"translated":"Ya"}`, `{"translated":"Done","missing":"English"}`, "{\n  \"missing\": \"English\"\n}\n"},
		{"target covers all", "a.jar", `{"translated":"Ya"}`, `{"translated":"Done"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			modpack, jar := testModpack(t)
			os.Remove(jar)
			sourceName := "z.jar"
			if tt.targetName == "z.jar" {
				sourceName = "a.jar"
			}
			writeTestJar(t, filepath.Join(modpack, "mods", sourceName), []zipEntry{{"assets/example/lang/en_us.json", []byte(tt.source)}})
			writeTestJar(t, filepath.Join(modpack, "mods", tt.targetName), []zipEntry{{"assets/example/lang/es_es.json", []byte(tt.target)}})
			if err := run([]string{modpack}); err != nil {
				t.Fatal(err)
			}
			workspace, _ := outputPaths(modpack)
			pending := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
			if tt.want == "" {
				assertAbsent(t, pending)
			} else if got := string(mustRead(t, pending)); got != tt.want {
				t.Fatalf("pending = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunDuplicateTargetKeysSuppressOnce(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target string
	}{
		{name: "same values", target: `{"translated":"Ya","translated":"Ya"}`},
		{name: "different values", target: `{"translated":"Uno","translated":"Dos"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			modpack, jar := testModpack(t)
			writeTestJar(t, jar, []zipEntry{
				{"assets/example/lang/es_es.json", []byte(tt.target)},
			})
			writeTestJar(t, filepath.Join(modpack, "mods", "source.jar"), []zipEntry{
				{"assets/example/lang/en_us.json", []byte(`{"translated":"English","missing":"Pending"}`)},
			})
			if err := run([]string{modpack}); err != nil {
				t.Fatal(err)
			}
			workspace, _ := outputPaths(modpack)
			if got := string(mustRead(t, filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName()))); got != "{\n  \"missing\": \"Pending\"\n}\n" {
				t.Fatalf("pending = %q", got)
			}
		})
	}
}

func TestDuplicateSourceKeyCoveredByTarget(t *testing.T) {
	for _, tt := range []struct {
		name    string
		entries []zipEntry
	}{
		{
			name: "same JAR",
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", []byte(`{"covered":"first","missing":"Pending","covered":"second"}`)},
				{"assets/example/lang/es_es.json", []byte(`{"covered":"Traducido"}`)},
			},
		},
		{
			name: "different duplicate values",
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", []byte(`{"covered":"must not leak","covered":"also must not leak"}`)},
				{"assets/example/lang/es_es.json", []byte(`{"covered":"Traducido"}`)},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			jar := filepath.Join(root, "mod.jar")
			writeTestJar(t, jar, tt.entries)
			output := filepath.Join(root, "output")
			if err := processMod(jar, output); err != nil {
				t.Fatal(err)
			}
			pending := filepath.Join(output, "assets", "example", "lang", pendingTranslationFileName())
			if tt.name == "same JAR" {
				if got := string(mustRead(t, pending)); got != "{\n  \"missing\": \"Pending\"\n}\n" {
					t.Fatalf("pending = %q", got)
				}
			} else {
				assertAbsent(t, pending)
			}
		})
	}
}

func TestDuplicateSourceKeyCoveredAcrossJarsRegardlessOfOrder(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jar")
	target := filepath.Join(root, "target.jar")
	writeTestJar(t, source, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"covered":"one","covered":"two","missing":"Pending"}`)}})
	writeTestJar(t, target, []zipEntry{{"assets/example/lang/es_es.json", []byte(`{"covered":"Traducido"}`)}})
	for _, order := range [][]string{{source, target}, {target, source}} {
		output := t.TempDir()
		aggregator := newLanguageAggregator(output)
		for _, jar := range order {
			if err := aggregator.addJar(jar); err != nil {
				t.Fatal(err)
			}
		}
		if err := aggregator.publish(); err != nil {
			t.Fatal(err)
		}
		if got := string(mustRead(t, filepath.Join(output, "assets", "example", "lang", pendingTranslationFileName()))); got != "{\n  \"missing\": \"Pending\"\n}\n" {
			t.Fatalf("pending = %q", got)
		}
	}
}

func TestCreateCentralKitchenDuplicateSourceAndTargetFixtures(t *testing.T) {
	source := mustRead(t, filepath.Join("testdata", "create_central_kitchen_en_us.json"))
	target := mustRead(t, filepath.Join("testdata", "create_central_kitchen_es_es.json"))
	root := t.TempDir()
	jar := filepath.Join(root, "create_central_kitchen.jar")
	writeTestJar(t, jar, []zipEntry{
		{"assets/create_central_kitchen/lang/en_us.json", source},
		{"assets/create_central_kitchen/lang/es_es.json", target},
	})
	output := filepath.Join(root, "output")
	if err := processMod(jar, output); err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, filepath.Join(output, "assets", "create_central_kitchen", "lang", pendingTranslationFileName()))
}

func TestCreateCentralKitchenDuplicateTargetFixture(t *testing.T) {
	target, err := os.ReadFile(filepath.Join("testdata", "create_central_kitchen_es_es.json"))
	if err != nil {
		t.Fatal(err)
	}
	keys, _, err := collectTargetLanguageKeys(target)
	if err != nil {
		t.Fatal(err)
	}
	if !keys["_"] || !keys["gui.create_central_kitchen.jei.category.kitchen"] || len(keys) != 2 {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestRunMalformedSameJarEnglishRollsBack(t *testing.T) {
	modpack, jar := testModpack(t)
	writeTestJar(t, jar, []zipEntry{{"assets/previous/lang/en_us.json", []byte(`{"old":"complete"}`)}})
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	workspace, _ := outputPaths(modpack)
	before := workspaceSnapshot(t, workspace)
	writeTestJar(t, jar, []zipEntry{{"assets/example/lang/es_es.json", []byte(`{"translated":"Ya"}`)}, {"assets/example/lang/en_us.json", []byte(`{"broken":`)}})
	err := run([]string{modpack})
	if err == nil || !strings.Contains(err.Error(), "assets/example/lang/en_us.json in mod.jar") || !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("run() error = %v, want same-JAR source context and parse failure", err)
	}
	if workspaceSnapshot(t, workspace) != before {
		t.Fatal("failed extraction changed live workspace")
	}
}

func TestLanguageEntrySizeLimit(t *testing.T) {
	for _, tt := range []struct {
		name, language string
	}{
		{name: "oversized source", language: sourceLanguageFileName()},
		{name: "oversized target", language: targetLanguageFileName()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			jar := filepath.Join(root, "oversized.jar")
			writeRepeatedTestJar(t, jar, "assets/example/lang/"+tt.language, maxPendingSourceFileSize+1)
			err := newLanguageAggregator(filepath.Join(root, "output")).addJar(jar)
			if err == nil || !strings.Contains(err.Error(), "assets/example/lang/"+tt.language+" in oversized.jar") || !strings.Contains(err.Error(), "uncompressed size") {
				t.Fatalf("addJar() error = %v, want JAR/entry context and declared-size rejection", err)
			}
		})
	}
}

func TestLanguageEntrySizeLimitAcceptsBoundary(t *testing.T) {
	root := t.TempDir()
	jar := filepath.Join(root, "boundary.jar")
	data := append([]byte(`{"key":"`), bytes.Repeat([]byte{'x'}, maxPendingSourceFileSize-len(`{"key":""}`))...)
	data = append(data, []byte(`"}`)...)
	writeTestJar(t, jar, []zipEntry{{"assets/example/lang/en_us.json", data}})
	if err := newLanguageAggregator(filepath.Join(root, "output")).addJar(jar); err != nil {
		t.Fatalf("addJar() boundary error = %v", err)
	}
}

func TestLanguageAggregateLimits(t *testing.T) {
	entry := []byte(`{"key":"value"}`)
	for _, tt := range []struct {
		name    string
		limits  languageLimits
		entries []zipEntry
		want    string
	}{
		{
			name:   "exact file count accepted",
			limits: languageLimits{files: 2, bytes: 1 << 20, members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/one/lang/en_us.json", entry},
				{"assets/two/lang/en_us.json", entry},
			},
		},
		{
			name:   "one above file count rejected",
			limits: languageLimits{files: 1, bytes: 1 << 20, members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/one/lang/en_us.json", entry},
				{"assets/two/lang/en_us.json", entry},
			},
			want: "language file count limit 1 exceeded at assets/two/lang/en_us.json in aggregate.jar",
		},
		{
			name:   "exact combined source and target bytes accepted",
			limits: languageLimits{files: 2, bytes: uint64(len(entry) * 2), members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", entry},
				{"assets/example/lang/es_es.json", entry},
			},
		},
		{
			name:   "one above combined source and target bytes rejected",
			limits: languageLimits{files: 2, bytes: uint64(len(entry)*2 - 1), members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", entry},
				{"assets/example/lang/es_es.json", entry},
			},
			want: "language byte limit " + strconv.Itoa(len(entry)*2-1) + " exceeded at assets/example/lang/es_es.json in aggregate.jar",
		},
		{
			name:   "duplicate target entries still count",
			limits: languageLimits{files: 1, bytes: 1 << 20, members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", entry},
				{"assets/example/lang/es_es.json", entry},
			},
			want: "language file count limit 1 exceeded at assets/example/lang/es_es.json in aggregate.jar",
		},
		{
			name:   "target-suppressed source still counts",
			limits: languageLimits{files: 2, bytes: uint64(len(entry)*2 - 1), members: maxCatalogEntries},
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", entry},
				{"assets/example/lang/en_us.json", entry},
			},
			want: "language byte limit " + strconv.Itoa(len(entry)*2-1) + " exceeded at assets/example/lang/en_us.json in aggregate.jar",
		},
		{
			name:    "exact combined member boundary accepted",
			limits:  languageLimits{files: 2, bytes: 1 << 20, members: 2},
			entries: []zipEntry{{"assets/example/lang/es_es.json", entry}, {"assets/example/lang/en_us.json", entry}},
		},
		{
			name:    "suppressed source one over member limit rejected",
			limits:  languageLimits{files: 2, bytes: 1 << 20, members: 1},
			entries: []zipEntry{{"assets/example/lang/es_es.json", entry}, {"assets/example/lang/en_us.json", entry}},
			want:    "language member limit 1 exceeded at assets/example/lang/en_us.json in aggregate.jar",
		},
		{
			name:    "duplicate target members count separately",
			limits:  languageLimits{files: 1, bytes: 1 << 20, members: 1},
			entries: []zipEntry{{"assets/example/lang/es_es.json", []byte(`{"key":"one","key":"two"}`)}},
			want:    "language member limit 1 exceeded at assets/example/lang/es_es.json in aggregate.jar",
		},
		{
			name:    "overwritten source members count separately",
			limits:  languageLimits{files: 2, bytes: 1 << 20, members: 1},
			entries: []zipEntry{{"assets/example/lang/en_us.json", entry}, {"assets/example/lang/en_us.json", entry}},
			want:    "language member limit 1 exceeded at assets/example/lang/en_us.json in aggregate.jar",
		},
		{
			name:    "duplicate source members count separately",
			limits:  languageLimits{files: 1, bytes: 1 << 20, members: 1},
			entries: []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"one","key":"two"}`)}},
			want:    "language member limit 1 exceeded at assets/example/lang/en_us.json in aggregate.jar",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			jar := filepath.Join(root, "aggregate.jar")
			writeTestJar(t, jar, tt.entries)
			err := newLanguageAggregatorWithLimits(filepath.Join(root, "output"), tt.limits).addJar(jar)
			if tt.want == "" && err != nil {
				t.Fatalf("addJar() error = %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("addJar() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunLanguageAggregateLimitPreservesWorkspace(t *testing.T) {
	modpack, jar := testModpack(t)
	writeTestJar(t, jar, []zipEntry{{"assets/previous/lang/en_us.json", []byte(`{"old":"complete"}`)}})
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	workspace, _ := outputPaths(modpack)
	before := workspaceSnapshot(t, workspace)

	writeTestJar(t, jar, []zipEntry{
		{"assets/example/lang/en_us.json", []byte(`{"one":"value"}`)},
		{"assets/example/lang/es_es.json", []byte(`{"one":"valor"}`)},
	})
	err := runWithLanguageLimits([]string{modpack}, languageLimits{files: 2, bytes: 1 << 20, members: 1})
	if err == nil || !strings.Contains(err.Error(), "language member limit 1 exceeded at assets/example/lang/es_es.json in mod.jar") {
		t.Fatalf("runWithLanguageLimits() error = %v, want aggregate limit with JAR/entry context", err)
	}
	if got := workspaceSnapshot(t, workspace); got != before {
		t.Fatal("aggregate limit failure changed live workspace")
	}
}

func TestRunMalformedTargetOrOtherJarEnglishRollsBack(t *testing.T) {
	for _, tt := range []struct {
		name, file string
		data       []byte
		want       string
	}{
		{"malformed target", targetLanguageFileName(), []byte(`{"broken":`), "EOF"},
		{"object target value", targetLanguageFileName(), []byte(`{"broken":{"nested":true}}`), `key "broken" must be a string`},
		{"array target value", targetLanguageFileName(), []byte(`{"broken":["nested"]}`), `key "broken" must be a string`},
		{"invalid UTF-8 target", targetLanguageFileName(), []byte{'{', '"', 0xff, '"', ':', '"', 'x', '"', '}'}, "file is not UTF-8"},
		{"malformed other English", sourceLanguageFileName(), []byte(`{"broken":`), "EOF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			modpack, jar := testModpack(t)
			writeTestJar(t, jar, []zipEntry{{"assets/previous/lang/en_us.json", []byte(`{"old":"complete"}`)}})
			if err := run([]string{modpack}); err != nil {
				t.Fatal(err)
			}
			workspace, _ := outputPaths(modpack)
			before := workspaceSnapshot(t, workspace)
			writeTestJar(t, jar, []zipEntry{{"assets/example/lang/es_es.json", []byte(`{"translated":"Ya"}`)}})
			writeTestJar(t, filepath.Join(modpack, "mods", "other.jar"), []zipEntry{{"assets/example/lang/" + tt.file, tt.data}})
			err := run([]string{modpack})
			context := "assets/example/lang/" + tt.file + " in other.jar"
			if err == nil || !strings.Contains(err.Error(), context) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() error = %v, want context %q and cause %q", err, context, tt.want)
			}
			if workspaceSnapshot(t, workspace) != before {
				t.Fatal("failed extraction changed live workspace")
			}
		})
	}
}

func TestLanguageAggregationIsDeterministicAcrossJarOrder(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.jar"), filepath.Join(root, "b.jar")
	writeTestJar(t, a, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"z":"last"}`)}})
	writeTestJar(t, b, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"a":"first"}`)}})
	var outputs [][]byte
	for _, order := range [][]string{{a, b}, {b, a}} {
		output := t.TempDir()
		aggregator := newLanguageAggregator(output)
		for _, jar := range order {
			if err := aggregator.addJar(jar); err != nil {
				t.Fatal(err)
			}
		}
		if err := aggregator.publish(); err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, mustRead(t, filepath.Join(output, "assets", "example", "lang", pendingTranslationFileName())))
	}
	if !bytes.Equal(outputs[0], outputs[1]) {
		t.Fatalf("outputs differ: %q != %q", outputs[0], outputs[1])
	}
}

func TestRunCatalogFailureRollsBackAndSuccessPublishesMatchingHash(t *testing.T) {
	modpack, jar := testModpack(t)
	writeTestJar(t, jar, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"old"}`)}, {"assets/example/patchouli_books/guide/en_us/entries/a.json", []byte(`{"name":"Old"}`)}})
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	workspace, _ := outputPaths(modpack)
	before := workspaceSnapshot(t, workspace)
	writeTestJar(t, jar, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"new"}`)}, {"assets/example/patchouli_books/guide/en_us/entries/a.json", []byte(`{"name":`)}})
	if err := run([]string{modpack}); err == nil {
		t.Fatal("malformed catalog source succeeded")
	}
	if workspaceSnapshot(t, workspace) != before {
		t.Fatal("catalog failure changed live workspace")
	}
	writeTestJar(t, jar, []zipEntry{{"assets/example/lang/en_us.json", []byte(`{"key":"new"}`)}})
	if err := run([]string{modpack}); err != nil {
		t.Fatal(err)
	}
	pending := mustRead(t, filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName()))
	digest := sha256.Sum256(pending)
	entry := readCatalog(t, filepath.Join(workspace, "catalog", "catalog.v1.json")).Entries[0]
	if entry.Source != "new" || entry.Writeback.SourceSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("published entry = %#v", entry)
	}
}

func testModpack(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "mods"), 0755)
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"minecraft":{"version":"1.20.1"}}`), 0644)
	return root, filepath.Join(root, "mods", "mod.jar")
}

func workspaceSnapshot(t *testing.T, workspace string) string {
	t.Helper()
	hash := sha256.New()
	for _, name := range []string{"assets", "sources", "catalog"} {
		filepath.Walk(filepath.Join(workspace, name), func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				hash.Write([]byte(path))
				hash.Write(mustRead(t, path))
			}
			return err
		})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestRemovePendingExportFilesDeletesOnlyOwnedPendingPaths(t *testing.T) {
	exportPackPath := t.TempDir()
	tests := []struct {
		name   string
		path   string
		delete bool
	}{
		{name: "owned pending language file", path: filepath.Join("assets", "example", "lang", pendingTranslationFileName()), delete: true},
		{name: "different pending file name", path: filepath.Join("assets", "example", "lang", "notes.pending.json")},
		{name: "pending file outside lang", path: filepath.Join("assets", "example", pendingTranslationFileName())},
		{name: "pending file below lang", path: filepath.Join("assets", "example", "lang", "nested", pendingTranslationFileName())},
		{name: "completed translation", path: filepath.Join("assets", "example", "lang", targetLanguageFileName())},
		{name: "unrelated user file", path: "notes.txt"},
	}

	for _, tt := range tests {
		path := filepath.Join(exportPackPath, tt.path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("create directory for %s: %v", tt.name, err)
		}
		if err := os.WriteFile(path, []byte(tt.name), 0644); err != nil {
			t.Fatalf("write %s: %v", tt.name, err)
		}
	}

	if err := removePendingExportFiles(exportPackPath); err != nil {
		t.Fatalf("removePendingExportFiles() error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := os.Stat(filepath.Join(exportPackPath, tt.path))
			if tt.delete && !os.IsNotExist(err) {
				t.Fatalf("owned pending file should be deleted, stat error = %v", err)
			}
			if !tt.delete && err != nil {
				t.Fatalf("unrelated file should be preserved, stat error = %v", err)
			}
		})
	}
}

func TestOutputPaths(t *testing.T) {
	modpackPath := filepath.Join("root", "modpack")
	workspacePath, exportPackPath := outputPaths(modpackPath)
	if want := filepath.Join(modpackPath, outputDirectory, "workspace"); workspacePath != want {
		t.Errorf("workspace path = %q, want %q", workspacePath, want)
	}
	if want := filepath.Join(modpackPath, outputDirectory, "export", "overrides", "resourcepacks", resourcePackName); exportPackPath != want {
		t.Errorf("export pack path = %q, want %q", exportPackPath, want)
	}
}

func TestCommonModpackSearchRootsStartsWithCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})

	roots := commonModpackSearchRoots()
	if len(roots) == 0 {
		t.Fatal("commonModpackSearchRoots() returned no roots")
	}
	if roots[0] != root {
		t.Fatalf("first search root = %q, want current directory %q", roots[0], root)
	}
}

func TestModpackPathFromArgsUsesCurrentDirectoryWhenItIsAModpack(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "mods"), 0755); err != nil {
		t.Fatalf("create mods directory: %v", err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})

	output, err := captureStdout(t, func() error {
		got, err := modpackPathFromArgs(nil)
		if err != nil {
			return err
		}
		if got != root {
			t.Fatalf("modpackPathFromArgs(nil) = %q, want current directory %q", got, root)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("modpackPathFromArgs(nil) error = %v", err)
	}
	if !strings.Contains(output, "Modpack detectado:") {
		t.Fatalf("output = %q, want detected modpack message", output)
	}
}

func TestSelectModpack(t *testing.T) {
	candidates := []string{
		filepath.Clean("C:/packs/alpha"),
		filepath.Clean("C:/packs/beta"),
	}
	var output bytes.Buffer

	got, err := selectModpack(candidates, strings.NewReader("2\n"), &output)
	if err != nil {
		t.Fatalf("selectModpack() error = %v", err)
	}
	if got != candidates[1] {
		t.Fatalf("selectModpack() = %q, want %q", got, candidates[1])
	}
	for _, want := range []string{
		"Se encontraron varios modpacks:",
		"1. " + candidates[0],
		"2. " + candidates[1],
		"Elige un modpack por número:",
		"Modpack seleccionado: " + candidates[1],
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want it to contain %q", output.String(), want)
		}
	}
}

func TestSelectModpackRejectsInvalidChoice(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "not a number", input: "abc\n"},
		{name: "out of range", input: "3\n"},
		{name: "empty input", input: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := selectModpack([]string{"alpha", "beta"}, strings.NewReader(tt.input), io.Discard)
			if err == nil {
				t.Fatal("selectModpack() error = nil, want error")
			}
		})
	}
}

func TestFindModpacksInRoots(t *testing.T) {
	root := t.TempDir()
	direct := filepath.Join(root, "direct")
	child := filepath.Join(root, "instances", "child")
	nested := filepath.Join(root, "instances", "nested", ".minecraft")
	for _, path := range []string{
		filepath.Join(direct, "mods"),
		filepath.Join(child, "mods"),
		filepath.Join(nested, "mods"),
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("create mods directory: %v", err)
		}
	}

	got, err := findModpacksInRoots([]string{
		direct,
		filepath.Join(root, "instances"),
		direct,
		filepath.Join(root, "missing"),
	})
	if err != nil {
		t.Fatalf("findModpacksInRoots() error = %v", err)
	}

	want := []string{
		filepath.Clean(child),
		filepath.Clean(direct),
		filepath.Clean(nested),
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %#v, want %#v", got, want)
		}
	}
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer
	runErr := run()
	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(output), runErr
}

func writeTestJar(t *testing.T, path string, entries []zipEntry) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create JAR: %v", err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		entryWriter, err := writer.Create(entry.name)
		if err != nil {
			t.Fatalf("create ZIP entry: %v", err)
		}
		if _, err := entryWriter.Write(entry.data); err != nil {
			t.Fatalf("write ZIP entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close JAR: %v", err)
	}
}

func writeRepeatedTestJar(t *testing.T, jarPath, entryName string, size int64) {
	t.Helper()
	file, err := os.Create(jarPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(entry, zeroReader{}, size); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(data []byte) (int, error) {
	clear(data)
	return len(data), nil
}
