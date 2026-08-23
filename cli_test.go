package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRootCommandParsesTranslationInvocation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		path      string
		translate bool
		refresh   bool
		force     bool
		wantError bool
	}{
		{name: "default extraction", args: []string{"pack"}, path: "pack"},
		{name: "translate before path", args: []string{"--translate", "pack"}, path: "pack", translate: true},
		{name: "translate after path", args: []string{"pack", "--translate"}, path: "pack", translate: true},
		{name: "translate explicit true", args: []string{"--translate=true", "pack"}, path: "pack", translate: true},
		{name: "translate with discovery", args: []string{"--translate"}, translate: true},
		{name: "refresh before path", args: []string{"--refresh", "pack"}, path: "pack", refresh: true},
		{name: "refresh after path", args: []string{"pack", "--refresh"}, path: "pack", refresh: true},
		{name: "refresh explicit true", args: []string{"--refresh=true", "pack"}, path: "pack", refresh: true},
		{name: "force before path", args: []string{"--force", "pack"}, path: "pack", force: true},
		{name: "force after path", args: []string{"pack", "--force"}, path: "pack", force: true},
		{name: "force explicit true", args: []string{"--force=true", "pack"}, path: "pack", force: true},
		{name: "translate refresh", args: []string{"--translate", "--refresh", "pack"}, path: "pack", translate: true, refresh: true},
		{name: "unknown flag", args: []string{"--other"}, wantError: true},
		{name: "multiple paths", args: []string{"one", "two"}, wantError: true},
		{name: "duplicate flag", args: []string{"--translate", "--translate"}, wantError: true},
		{name: "duplicate flag with explicit value", args: []string{"--translate", "--translate=true"}, wantError: true},
		{name: "duplicate refresh", args: []string{"--refresh", "--refresh"}, wantError: true},
		{name: "duplicate force", args: []string{"--force", "--force"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got cliOptions
			called := false
			command := newRootCommand(commandDependencies{languageLimits: languageLimits{}, getenv: func(string) string { return "" }, output: &bytes.Buffer{}, runTranslation: func(options cliOptions, _ languageLimits) error {
				got = options
				called = true
				return nil
			}})
			command.SetArgs(tt.args)
			err := command.Execute()
			if (err != nil) != tt.wantError {
				t.Fatalf("Execute() error = %v", err)
			}
			if err == nil && (!called || got.modpackPath != tt.path || got.translate != tt.translate || got.refresh != tt.refresh || got.force != tt.force) {
				t.Fatalf("translation call = %v, options = %#v", called, got)
			}
		})
	}
}

func TestModelsSubcommandDoesNotRunTranslation(t *testing.T) {
	translated := false
	env := map[string]string{"PROVIDER_CHAIN": "broken"}
	var output bytes.Buffer
	command := newRootCommand(commandDependencies{languageLimits: languageLimits{}, getenv: func(name string) string { return env[name] }, output: &output, runTranslation: func(cliOptions, languageLimits) error {
		translated = true
		return nil
	}})
	command.SetArgs([]string{"models"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "model diagnostics failed") {
		t.Fatalf("Execute() error = %v, want models diagnostic error", err)
	}
	if !strings.Contains(output.String(), "Provider: broken") {
		t.Fatalf("models output missing provider result:\n%s", output.String())
	}
	if translated {
		t.Fatal("models subcommand ran root translation")
	}
}

func TestRootCommandRejectsDuplicateFlags(t *testing.T) {
	for _, name := range []string{"translate", "refresh", "force"} {
		t.Run(name, func(t *testing.T) {
			command := newRootCommand(commandDependencies{languageLimits: languageLimits{}, getenv: func(string) string { return "" }, output: &bytes.Buffer{}, runTranslation: func(cliOptions, languageLimits) error {
				t.Fatal("duplicate flags reached translation")
				return nil
			}})
			command.SetArgs([]string{"--" + name, "--" + name + "=true"})
			err := command.Execute()
			want := "--" + name + " may only be specified once"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Execute() error = %v, want error containing %q", err, want)
			}
		})
	}
}

func TestModelsCommandPrintsEveryProviderAndMarksConfiguredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"other"},{"id":"configured"}]}`)
	}))
	defer server.Close()
	env := map[string]string{
		"PROVIDER_CHAIN":           "broken,ollama",
		"PROVIDER_OLLAMA_BASE_URL": server.URL,
		"PROVIDER_OLLAMA_MODEL":    "configured",
		"PROVIDER_OLLAMA_TIMEOUT":  "1s",
		"PROVIDER_OLLAMA_MODE":     "json_schema",
	}
	var output bytes.Buffer
	command := newModelsCommand(func(name string) string { return env[name] }, &output)
	err := command.Execute()
	if err == nil {
		t.Fatal("mixed provider result unexpectedly succeeded")
	}
	got := output.String()
	for _, want := range []string{
		"Provider: broken",
		"Error: PROVIDER_BROKEN_BASE_URL is required for provider broken",
		"Provider: ollama",
		"Configured model: configured (available)",
		"- configured (configured)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "Provider: broken") > strings.Index(got, "Provider: ollama") {
		t.Fatalf("provider output order changed:\n%s", got)
	}
}
