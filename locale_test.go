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
		input   string
		want    string
		prompt  string
		wantErr bool
	}{
		{name: "accepts detected default", env: map[string]string{"LANG": "fr_FR.UTF-8"}, input: "\n", want: "fr_fr", prompt: "Introduzca idioma al que traducir [fr_fr]: "},
		{name: "ignores unsupported detected default", env: map[string]string{"LANG": "xx_YY.UTF-8"}, input: "\n", want: "es_es", prompt: "Introduzca idioma al que traducir [es_es]: "},
		{name: "falls back to Spanish", env: map[string]string{}, input: "\n", want: "es_es", prompt: "Introduzca idioma al que traducir [es_es]: "},
		{name: "normalizes typed locale", env: map[string]string{"LANG": "fr_FR.UTF-8"}, input: "pt-BR\n", want: "pt_br", prompt: "Introduzca idioma al que traducir [fr_fr]: "},
		{name: "rejects valid shape unsupported by minecraft", env: map[string]string{}, input: "xx_yy\n", wantErr: true, prompt: "Introduzca idioma al que traducir [es_es]: "},
		{name: "rejects invalid typed locale", env: map[string]string{}, input: "spanish\n", wantErr: true, prompt: "Introduzca idioma al que traducir [es_es]: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := selectTargetLocale(strings.NewReader(tt.input), &output, func(name string) string { return tt.env[name] })
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
