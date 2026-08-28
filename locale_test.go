package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeMinecraftLocale(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{name: "hyphen", input: "es-ES", want: "es_es", ok: true},
		{name: "encoding suffix", input: "es_ES.UTF-8", want: "es_es", ok: true},
		{name: "already minecraft", input: "es_es", want: "es_es", ok: true},
		{name: "minecraft language without region", input: "tok", want: "tok", ok: true},
		{name: "minecraft fantasy language", input: "tlh-AA", want: "tlh_aa", ok: true},
		{name: "unsupported language only", input: "es", ok: false},
		{name: "unsupported long region", input: "es_419", ok: false},
		{name: "unsupported non ascii", input: "es_EÑ", ok: false},
		{name: "valid shape but not minecraft", input: "xx_yy", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeMinecraftLocale(tt.input)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("normalizeMinecraftLocale(%q) = %q, %v; want %q, %v", tt.input, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSelectTargetLocalePromptsWithDefault(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		system  string
		input   string
		want    string
		prompt  string
		wantErr bool
	}{
		{name: "accepts detected default", env: map[string]string{"LANG": "fr_FR.UTF-8"}, input: "\n", want: "fr_fr", prompt: "Enter target language [fr_fr]: "},
		{name: "uses system locale after unsupported environment", env: map[string]string{"LANG": "xx_YY.UTF-8"}, system: "de-DE", input: "\n", want: "de_de", prompt: "Enter target language [de_de]: "},
		{name: "falls back to Spanish", env: map[string]string{}, input: "\n", want: "es_es", prompt: "Enter target language [es_es]: "},
		{name: "normalizes typed locale", env: map[string]string{"LANG": "fr_FR.UTF-8"}, input: "pt-BR\n", want: "pt_br", prompt: "Enter target language [fr_fr]: "},
		{name: "rejects valid shape unsupported by minecraft", env: map[string]string{}, input: "xx_yy\n", wantErr: true, prompt: "Enter target language [es_es]: "},
		{name: "rejects invalid typed locale", env: map[string]string{}, input: "spanish\n", wantErr: true, prompt: "Enter target language [es_es]: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := selectTargetLocale(
				strings.NewReader(tt.input),
				&output,
				func(name string) string { return tt.env[name] },
				func() string { return tt.system },
			)
			if tt.wantErr {
				if err == nil {
					t.Fatal("selectTargetLocale() error = nil")
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("selectTargetLocale() = %q, %v; want %q", got, err, tt.want)
			}
			if output.String() != tt.prompt {
				t.Fatalf("prompt = %q, want %q", output.String(), tt.prompt)
			}
		})
	}
}

func TestDetectDefaultTargetLocale(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		system string
		want   string
	}{
		{name: "VS Code LANG cannot mask Windows system locale", env: map[string]string{"LANG": "en_US.UTF-8"}, system: "es-ES", want: "es_es"},
		{name: "LC_ALL overrides system locale", env: map[string]string{"LC_ALL": "pt_BR", "LANG": "fr_FR"}, system: "de-DE", want: "pt_br"},
		{name: "LC_MESSAGES overrides system locale", env: map[string]string{"LC_MESSAGES": "fr_FR.UTF-8", "LANG": "en_US.UTF-8"}, system: "de-DE", want: "fr_fr"},
		{name: "LC_ALL wins over LC_MESSAGES", env: map[string]string{"LC_ALL": "pt_BR", "LC_MESSAGES": "fr_FR"}, system: "de-DE", want: "pt_br"},
		{name: "Windows system locale is converted", env: map[string]string{}, system: "es-ES", want: "es_es"},
		{name: "LANG is used when system locale is unsupported", env: map[string]string{"LANG": "fr_FR.UTF-8"}, system: "xx-YY", want: "fr_fr"},
		{name: "LANGUAGE list is used when system locale is missing", env: map[string]string{"LANGUAGE": "xx_YY:ja_JP:fr_FR"}, want: "ja_jp"},
		{name: "LANG wins over LANGUAGE fallback", env: map[string]string{"LANG": "fr_FR", "LANGUAGE": "ja_JP"}, want: "fr_fr"},
		{name: "unsupported environment and system locales fall back", env: map[string]string{"LANG": "xx_YY"}, system: "zz-ZZ", want: defaultTargetLanguageCode},
		{name: "missing system locale falls back", env: map[string]string{}, want: defaultTargetLanguageCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDefaultTargetLocale(
				func(name string) string { return tt.env[name] },
				func() string { return tt.system },
			)
			if got != tt.want {
				t.Fatalf("detectDefaultTargetLocale() = %q, want %q", got, tt.want)
			}
		})
	}
}
