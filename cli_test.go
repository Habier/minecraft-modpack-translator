package main

import "testing"

func TestParseCLI(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		path      string
		translate bool
		refresh   bool
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
		{name: "translate refresh", args: []string{"--translate", "--refresh", "pack"}, path: "pack", translate: true, refresh: true},
		{name: "unknown flag", args: []string{"--other"}, wantError: true},
		{name: "multiple paths", args: []string{"one", "two"}, wantError: true},
		{name: "duplicate flag", args: []string{"--translate", "--translate"}, wantError: true},
		{name: "duplicate flag with explicit value", args: []string{"--translate", "--translate=true"}, wantError: true},
		{name: "duplicate refresh", args: []string{"--refresh", "--refresh"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCLI(tt.args)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseCLI() error = %v", err)
			}
			if err == nil && (got.modpackPath != tt.path || got.translate != tt.translate || got.refresh != tt.refresh) {
				t.Fatalf("parseCLI() = %#v", got)
			}
		})
	}
}
