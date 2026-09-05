package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modpack-translator/internal/provider"
)

func TestApplicationLogCreatesCorrelatedFilesAndReportsPaths(t *testing.T) {
	modpackPath := t.TempDir()
	startedAt := time.Date(2026, time.August, 31, 12, 34, 56, 789000000, time.UTC)
	appLog, err := startApplicationLogAt(modpackPath, startedAt, 4242)
	if err != nil {
		t.Fatalf("startApplicationLogAt() error = %v", err)
	}
	defer func() { _ = appLog.close() }()

	wantRunID := "20260831T123456.789Z-4242"
	if appLog.runID != wantRunID || !strings.Contains(filepath.Base(appLog.translationPath), wantRunID) || !strings.Contains(filepath.Base(appLog.providerPath), wantRunID) {
		t.Fatalf("uncorrelated paths: run_id=%q translation=%q provider=%q", appLog.runID, appLog.translationPath, appLog.providerPath)
	}
	if !filepath.IsAbs(appLog.translationPath) || !filepath.IsAbs(appLog.providerPath) {
		t.Fatalf("reported paths must be absolute: %q %q", appLog.translationPath, appLog.providerPath)
	}
	var output bytes.Buffer
	appLog.reportPaths(&output)
	for _, want := range []string{"Session log: " + appLog.translationPath, "Provider diagnostics: " + appLog.providerPath} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("startup output %q does not contain %q", output.String(), want)
		}
	}
	if got := appLog.providerBuildOptions(false); got != (provider.BuildOptions{}) {
		t.Fatalf("default provider build options = %#v", got)
	}
	debug := appLog.providerBuildOptions(true)
	if !debug.Debug || debug.InvalidResponseQuarantineDirectory != filepath.Join(filepath.Dir(appLog.providerPath), "invalid-provider-responses") || debug.RunID != appLog.runID {
		t.Fatalf("debug provider build options = %#v", debug)
	}
}

func TestApplicationLogCollisionKeepsFilesCorrelatedAndCleansOrphan(t *testing.T) {
	modpackPath := t.TempDir()
	startedAt := time.Date(2026, time.August, 31, 12, 34, 56, 789000000, time.UTC)
	baseRunID := "20260831T123456.789Z-4242"
	logDir := filepath.Join(modpackPath, outputDirectory, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	blockingPath := filepath.Join(logDir, "provider-failures-"+baseRunID+".jsonl")
	if err := os.WriteFile(blockingPath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	appLog, err := startApplicationLogAt(modpackPath, startedAt, 4242)
	if err != nil {
		t.Fatalf("startApplicationLogAt() error = %v", err)
	}
	if err := appLog.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	if appLog.runID != baseRunID+"-001" {
		t.Fatalf("run_id = %q", appLog.runID)
	}
	if _, err := os.Stat(filepath.Join(logDir, "translation-"+baseRunID+".log")); !os.IsNotExist(err) {
		t.Fatalf("orphan translation log remains: %v", err)
	}
	if data, err := os.ReadFile(blockingPath); err != nil || string(data) != "existing" {
		t.Fatalf("collision file changed: %q, %v", data, err)
	}
}

func TestApplicationLogCapturesProcessOutputAndStructuredEvents(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr
	appLog, err := startApplicationLog(t.TempDir())
	if err != nil {
		t.Fatalf("startApplicationLog() error = %v", err)
	}
	if os.Stdout == stdout || os.Stderr == stderr {
		t.Fatal("application logging did not install process output capture")
	}
	fmt.Fprintln(os.Stdout, "ordinary stdout")
	fmt.Fprintln(os.Stderr, "ordinary stderr")
	appLog.Event(context.Background(), "translation_progress", slog.Int("translated", 3), slog.Int("remaining", 2))
	if err := appLog.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("application logging did not restore process output")
	}
	data, err := os.ReadFile(appLog.translationPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ordinary stdout", "ordinary stderr", "msg=translation_progress", "translated=3", "remaining=2"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("human log %q does not contain %q", data, want)
		}
	}
}

func TestProviderHTTPFailureWritesFailClosedJSONL(t *testing.T) {
	const sensitive = `quoted source fragment, translated content, and Bearer secret-token`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "invalid_parameter", "message": sensitive}})
	}))
	defer server.Close()

	appLog, err := startApplicationLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = appLog.close()
		}
	}()
	values := map[string]string{"PROVIDER_CHAIN": "ollama", "PROVIDER_OLLAMA_BASE_URL": server.URL, "PROVIDER_OLLAMA_MODEL": "model", "PROVIDER_OLLAMA_MODE": "json_object", "PROVIDER_OLLAMA_TIMEOUT": "5s"}
	translator, _, err := provider.BuildChain(func(key string) string { return values[key] }, &bytes.Buffer{}, appLog, provider.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = translator.Translate(context.Background(), []provider.Request{{ID: "a", Source: "source fragment", TargetLocale: "es_es"}})
	if err := appLog.close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	data, err := os.ReadFile(appLog.providerPath)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode JSONL: %v", err)
	}
	for key, want := range map[string]any{"schema": "modpack-translator.provider-failure/v1", "run_id": appLog.runID, "provider": "ollama", "model": "model", "http_status": float64(422), "kind": "invalid_request", "provider_code": "invalid_parameter", "details_omitted": true, "batch_size": float64(1), "attempt": float64(1), "transition_target": ""} {
		if record[key] != want {
			t.Errorf("record[%q] = %#v, want %#v", key, record[key], want)
		}
	}
	for _, forbidden := range []string{sensitive, "source fragment", "translated content", "secret-token", "Bearer"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("provider log exposed %q: %s", forbidden, data)
		}
	}
}

func TestApplicationLogCloseErrorIsPropagated(t *testing.T) {
	operationErr := errors.New("translation failed")
	closeErr := errors.New("flush failed")
	got := propagateApplicationLogClose(operationErr, stubApplicationLogCloser{err: closeErr})
	if !errors.Is(got, operationErr) || !errors.Is(got, closeErr) || !strings.Contains(got.Error(), "close application logs") {
		t.Fatalf("propagated error = %v", got)
	}
}

type stubApplicationLogCloser struct{ err error }

func (s stubApplicationLogCloser) close() error { return s.err }

func TestApplicationLogDoesNotCreateProcessRelativeTranslationLog(t *testing.T) {
	workingDirectory := t.TempDir()
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldDirectory) }()

	appLog, err := startApplicationLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appLog.Event(context.Background(), "translation_start")
	if err := appLog.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, "translation.log")); !os.IsNotExist(err) {
		t.Fatalf("process-relative translation.log exists or stat failed: %v", err)
	}
}
