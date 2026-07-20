package main

import "testing"

func TestParseCLI(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		path      string
		translate bool
		wantError bool
	}{
		{name: "default extraction", args: []string{"pack"}, path: "pack"},
		{name: "translate before path", args: []string{"--translate", "pack"}, path: "pack", translate: true},
		{name: "translate after path", args: []string{"pack", "--translate"}, path: "pack", translate: true},
		{name: "translate with discovery", args: []string{"--translate"}, translate: true},
		{name: "unknown flag", args: []string{"--other"}, wantError: true},
		{name: "multiple paths", args: []string{"one", "two"}, wantError: true},
		{name: "duplicate flag", args: []string{"--translate", "--translate"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCLI(tt.args)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseCLI() error = %v", err)
			}
			if err == nil && (got.modpackPath != tt.path || got.translate != tt.translate) {
				t.Fatalf("parseCLI() = %#v", got)
			}
		})
	}
}
