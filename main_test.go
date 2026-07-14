package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
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
			pending: map[string][]byte{"example": []byte("{\"key\":\"English\"}")},
			absent:  []string{"example"},
			output:  "[PENDING] mod.jar/example: assets/example/lang/es_es.pending.json",
		},
		{
			name: "Spanish wins when English appears last",
			entries: []zipEntry{
				{"assets/example/lang/es_es.json", []byte("spanish")},
				{"assets/example/lang/en_us.json", []byte("english")},
			},
			ok:     []string{"example"},
			absent: []string{"example"},
			output: "[OK] mod.jar/example: es_es.json existente",
		},
		{
			name: "Spanish wins when English appears first",
			entries: []zipEntry{
				{"assets/example/lang/en_us.json", []byte("english")},
				{"assets/example/lang/es_es.json", []byte("spanish")},
			},
			ok:     []string{"example"},
			absent: []string{"example"},
			output: "[OK] mod.jar/example: es_es.json existente",
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
				{"assets/alpha/lang/en_us.json", []byte("alpha english")},
				{"assets/beta/lang/en_us.json", []byte("beta english")},
				{"assets/beta/lang/es_es.json", []byte("beta spanish")},
				{"assets/gamma/lang/de_de.json", []byte("gamma german")},
			},
			ok: []string{"beta"},
			pending: map[string][]byte{
				"alpha": []byte("alpha english"),
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

				pendingPath := filepath.Join(outputPath, "assets", namespace, "lang", pendingTranslationFileName())
				if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
					t.Errorf("expected %s to be absent, stat error = %v", pendingPath, err)
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
