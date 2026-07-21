package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		missing       bool
		want          string
	}{
		{name: "missing file is optional", missing: true},
		{name: "valid file", content: "MODPACK_ENV_TEST=from-file\n", want: "from-file"},
		{name: "malformed file is rejected", content: "BROKEN='unterminated\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			os.Unsetenv("MODPACK_ENV_TEST")
			t.Cleanup(func() { os.Unsetenv("MODPACK_ENV_TEST") })
			err := loadEnvFile(path)
			if tt.name == "malformed file is rejected" {
				if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "unterminated") {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || os.Getenv("MODPACK_ENV_TEST") != tt.want {
				t.Fatalf("value=%q error=%v", os.Getenv("MODPACK_ENV_TEST"), err)
			}
		})
	}
}

func TestLoadEnvFileUnreadableWhereSupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions do not portably make a file unreadable")
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("SECRET=value\n"), 0000); err != nil {
		t.Fatal(err)
	}
	err := loadEnvFile(path)
	if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "value") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadEnvFileCWDfallback(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("MODPACK_ENV_TEST=from-cwd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original); os.Unsetenv("MODPACK_ENV_TEST") })
	if err := loadExecutableEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MODPACK_ENV_TEST") != "from-cwd" {
		t.Fatalf("got %q", os.Getenv("MODPACK_ENV_TEST"))
	}
}

func TestLoadEnvFileCWDfallbackProcessWins(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("MODPACK_ENV_TEST=file\nCWD_ONLY=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original); os.Unsetenv("MODPACK_ENV_TEST"); os.Unsetenv("CWD_ONLY") })
	t.Setenv("MODPACK_ENV_TEST", "process")
	if err := loadExecutableEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MODPACK_ENV_TEST") != "process" || os.Getenv("CWD_ONLY") != "secret" {
		t.Fatalf("MODPACK_ENV_TEST=%q CWD_ONLY=%q", os.Getenv("MODPACK_ENV_TEST"), os.Getenv("CWD_ONLY"))
	}
}

func TestLoadEnvFileMalformedCWDReturnsError(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("BROKEN='unterminated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })
	err := loadExecutableEnv()
	if err == nil || strings.Contains(err.Error(), "untterminated") || strings.Contains(err.Error(), "BROKEN") {
		t.Fatalf("error=%v", err)
	}
}
