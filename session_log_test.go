package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartSessionLogWritesStdoutAndStderrToSessionFile(t *testing.T) {
	modpackPath := t.TempDir()

	log, err := startSessionLog(modpackPath)
	if err != nil {
		t.Fatalf("startSessionLog() error = %v", err)
	}
	fmt.Fprintln(os.Stdout, "stdout line")
	fmt.Fprintln(os.Stderr, "stderr line")
	if err := log.close(); err != nil {
		t.Fatalf("close session log: %v", err)
	}

	if got, want := filepath.Dir(log.path), filepath.Join(modpackPath, outputDirectory, "logs"); got != want {
		t.Fatalf("log directory = %q, want %q", got, want)
	}
	if !strings.HasPrefix(filepath.Base(log.path), "session-") || !strings.HasSuffix(log.path, ".log") {
		t.Fatalf("log path = %q, want session log filename", log.path)
	}

	data, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "stdout line") || !strings.Contains(content, "stderr line") {
		t.Fatalf("session log content = %q, want stdout and stderr", content)
	}
}

func TestStartSessionLogCreatesDifferentFilePerSession(t *testing.T) {
	modpackPath := t.TempDir()

	first, err := startSessionLog(modpackPath)
	if err != nil {
		t.Fatalf("first startSessionLog() error = %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first log: %v", err)
	}

	second, err := startSessionLog(modpackPath)
	if err != nil {
		t.Fatalf("second startSessionLog() error = %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatalf("close second log: %v", err)
	}

	if first.path == second.path {
		t.Fatalf("session logs used the same path %q", first.path)
	}
}

func TestSessionLogCanRecordReturnedErrorBeforeClose(t *testing.T) {
	modpackPath := t.TempDir()

	log, err := startSessionLog(modpackPath)
	if err != nil {
		t.Fatalf("startSessionLog() error = %v", err)
	}
	log.writeError(os.ErrNotExist)
	if err := log.close(); err != nil {
		t.Fatalf("close session log: %v", err)
	}

	data, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !strings.Contains(string(data), "Error: file does not exist") {
		t.Fatalf("session log content = %q, want returned error", string(data))
	}
}
