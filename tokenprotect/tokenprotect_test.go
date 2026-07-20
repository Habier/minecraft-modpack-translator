package tokenprotect_test

import (
	"errors"
	"strings"
	"testing"

	"modpack-translator/tokenprotect"
)

func TestFind(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"mixed Minecraft and Patchouli", "§aHello %1$s$(br)Use {item}: minecraft:stone %%\n$(l:guide:intro)Open$()", []string{"§a", "%1$s", "$(br)", "{item}", "minecraft:stone", "%%", "\n", "$(l:guide:intro)", "$()"}},
		{"variables but not objects", `${name} {0} {player_name} and {"name":"Alex"}`, []string{"${name}", "{0}", "{player_name}"}},
		{"printf variants", "%s %d %2$08.2f %+d %x invalid %word", []string{"%s", "%d", "%2$08.2f", "%+d", "%x"}},
		{"date-time and newline printf", "%tY %1$tF %TM %n", []string{"%tY", "%1$tF", "%TM", "%n"}},
		{"malformed date-time printf", "%t %1$t %t? %1$t?", nil},
		{"format codes case insensitive", "§Acolor §Lbold §Rreset §z", []string{"§A", "§L", "§R"}},
		{"URL wins over resource", "See https://example.com/wiki/minecraft:stone?q=a and mod:path", []string{"https://example.com/wiki/minecraft:stone?q=a", "mod:path"}},
		{"Patchouli link wins over resource", "$(l:namespace:path)link$()", []string{"$(l:namespace:path)", "$()"}},
		{"Unicode prose", "Español: pulsa {tecla}; English: visit mod:guía", []string{"{tecla}"}},
		{"malformed left as prose", "bad $(br {name % 1$s §z upper:Path", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTokens := tokenprotect.Find(tt.source)
			got := make([]string, len(gotTokens))
			for i := range gotTokens {
				got[i] = gotTokens[i].Text
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("Find() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"plain prose",
		"§aHola %s$(br)Pulsa {key} for mod:item.$()",
		"Repeated %s then %s and minecraft:stone twice minecraft:stone",
		"Unicode español 日本語 ${player} https://example.org/a:b",
		"line one\nline two\\nline three\r\n",
	}
	for _, source := range cases {
		t.Run(source, func(t *testing.T) {
			protected, err := tokenprotect.Protect(source)
			if err != nil {
				t.Fatal(err)
			}
			if len(protected.Tokens()) > 0 {
				for _, token := range protected.Tokens() {
					if strings.Contains(protected.Protected, token.Text) {
						t.Fatalf("protected payload exposes token %q", token.Text)
					}
				}
			}
			got, err := protected.Restore(protected.Protected)
			if err != nil {
				t.Fatal(err)
			}
			if got != source {
				t.Fatalf("round trip = %q, want %q", got, source)
			}
		})
	}
}

func TestRestoreAllowsSafeReordering(t *testing.T) {
	protected, err := tokenprotect.Protect("First %1$s then %2$d using {item} and mod:path")
	if err != nil {
		t.Fatal(err)
	}
	markers := strings.Fields(protected.Protected)
	translated := "Segundo " + markers[3] + " primero " + markers[1] + " recurso " + markers[7] + " variable " + markers[5]
	got, err := protected.Restore(translated)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Segundo %2$d primero %1$s recurso mod:path variable {item}" {
		t.Fatalf("Restore() = %q", got)
	}
}

func TestRestoreRejectsInvalidMarkers(t *testing.T) {
	protected, err := tokenprotect.Protect("Value %s and {name}")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(protected.Protected)
	first, second := fields[1], fields[3]
	tests := []struct {
		name, translated, problem string
	}{
		{"missing", "Valor " + first, "missing marker"},
		{"duplicated", "Valor " + first + " " + first + " " + second, "duplicated marker"},
		{"unknown", "Valor " + first + " " + strings.Replace(second, "000001", "999999", 1), "unknown marker"},
		{"altered", "Valor " + first + " " + strings.TrimSuffix(second, "__") + "_", "malformed marker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := protected.Restore(tt.translated)
			var validationError *tokenprotect.Error
			if !errors.As(err, &validationError) || !strings.Contains(err.Error(), tt.problem) {
				t.Fatalf("Restore() error = %v, want %q", err, tt.problem)
			}
			if strings.Contains(err.Error(), "%s") || strings.Contains(err.Error(), "{name}") {
				t.Fatalf("error exposes source token: %v", err)
			}
		})
	}
}

func TestRestoreOrderPolicy(t *testing.T) {
	tests := []struct {
		name, source string
		wantError    bool
	}{
		{"unindexed printf order required", "%s then %d", true},
		{"indexed printf order movable", "%1$s then %2$d", false},
		{"Patchouli macro order required", "$(l:mod:page)link$()", true},
		{"Minecraft formatting order required", "§aColor §lbold", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protected, err := tokenprotect.Protect(tt.source)
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(protected.Protected)
			translated := fields[len(fields)-1] + " translated " + fields[0]
			_, err = protected.Restore(translated)
			if (err != nil) != tt.wantError {
				t.Fatalf("Restore() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestRepeatedIdenticalTokensHaveDistinctMarkers(t *testing.T) {
	protected, err := tokenprotect.Protect("%1$s and %1$s")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(protected.Protected)
	if fields[0] == fields[2] {
		t.Fatal("repeated occurrences must have distinct markers")
	}
	if _, err := protected.Restore(fields[2] + " y " + fields[0]); err != nil {
		t.Fatalf("indexed repeated placeholders may move: %v", err)
	}
}

func TestMarkerPrefixIsDeterministicAndAvoidsSourceCollision(t *testing.T) {
	base, err := tokenprotect.Protect("Hello %s")
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(base.Protected[len("Hello "):], "000000__")
	source := "Marker-like prose __MPT_not_a_marker_ and collision " + prefix + "000000__ %s"
	one, err := tokenprotect.Protect(source)
	if err != nil {
		t.Fatal(err)
	}
	two, err := tokenprotect.Protect(source)
	if err != nil {
		t.Fatal(err)
	}
	if one.Protected != two.Protected {
		t.Fatal("marker derivation is not deterministic")
	}
	if strings.Count(one.Protected, prefix) != 1 {
		t.Fatal("source collision text should remain prose and not be reused as a marker prefix")
	}
	if got, err := one.Restore(one.Protected); err != nil || got != source {
		t.Fatalf("collision round trip = %q, %v", got, err)
	}
}

func TestLargeInput(t *testing.T) {
	source := strings.Repeat("Long español prose %1$s $(br) mod:item https://example.org/x ", 10000)
	protected, err := tokenprotect.Protect(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := protected.Restore(protected.Protected)
	if err != nil || got != source {
		t.Fatalf("large round trip failed: length %d, error %v", len(got), err)
	}
}

func TestLargeTokenFreeNamespaceRun(t *testing.T) {
	source := strings.Repeat("namespace_like_text", 1<<16)
	if tokens := tokenprotect.Find(source); len(tokens) != 0 {
		t.Fatalf("Find() returned %d tokens", len(tokens))
	}
}
