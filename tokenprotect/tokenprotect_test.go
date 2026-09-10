package tokenprotect_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"modpack-translator/tokenprotect"
)

var sentinelPattern = regexp.MustCompile(`MPTK_[A-Z0-9]{16}_[0-9A-F]{8}_END`)

func sentinels(value string) []string {
	return sentinelPattern.FindAllString(value, -1)
}

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
		{"ampersand format codes", "&6Controller&r &l&o&cNO ORE&r", []string{"&6", "&r", "&l", "&o", "&c", "&r"}},
		{"ampersand format codes before lowercase text", "&oitalic &abold &rreset", []string{"&o", "&a", "&r"}},
		{"ordinary ampersands", "R&D, Tom & Jerry, and &copy;", nil},
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
		"&6Controller&r",
		"&oitalic &abold &rreset",
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

func TestRestoreRejectsInvalidPlaceholders(t *testing.T) {
	protected, err := tokenprotect.Protect("Value %s and {name}")
	if err != nil {
		t.Fatal(err)
	}
	markers := sentinels(protected.Protected)
	if len(markers) != 2 {
		t.Fatalf("sentinels = %#v", markers)
	}
	first, second := markers[0], markers[1]
	unknown := first[:22] + "FFFFFFFF_END"
	tests := []struct {
		name, translated, problem string
	}{
		{"missing", "Valor " + first, "missing placeholder"},
		{"duplicated", "Valor " + first + " " + first + " " + second, "duplicated placeholder"},
		{"unknown", "Valor " + first + " " + unknown, "unknown placeholder"},
		{"malformed index", "Valor " + first + " " + second[:22] + "ZZZZZZZZ_END", "malformed placeholder"},
		{"malformed suffix", "Valor " + first + " " + second[:30] + "_STOP", "malformed placeholder"},
		{"reordered", "Valor " + second + " " + first, "placeholders were reordered"},
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

func TestRestoreIgnoresBenignSentinelPrefixes(t *testing.T) {
	protected, err := tokenprotect.Protect("Value %s")
	if err != nil {
		t.Fatal(err)
	}
	marker := sentinels(protected.Protected)[0]
	translated := "MPTK_KEEP and MPTK_ABCDEFGHIJKLMNOPQ_00000000_END " + marker
	if _, err := protected.Restore(translated); err != nil {
		t.Fatalf("Restore(%q) rejected benign prefix text: %v", translated, err)
	}
}

func TestRepeatedIdenticalTokensHaveDistinctMarkers(t *testing.T) {
	protected, err := tokenprotect.Protect("%1$s and %1$s")
	if err != nil {
		t.Fatal(err)
	}
	markers := sentinels(protected.Protected)
	if len(markers) != 2 {
		t.Fatalf("sentinels = %#v", markers)
	}
	first, second := markers[0], markers[1]
	if !strings.Contains(protected.Protected, first) || !strings.Contains(protected.Protected, second) {
		t.Fatal("repeated occurrences must have distinct markers")
	}
	if _, err := protected.Restore(second + " y " + first); err == nil {
		t.Fatal("repeated placeholders must retain relative order")
	}
}

func TestSentinelNamespaceIsDeterministicAndAvoidsSourceCollision(t *testing.T) {
	base, err := tokenprotect.Protect("Hello %s")
	if err != nil {
		t.Fatal(err)
	}
	baseMarkers := sentinels(base.Protected)
	if len(baseMarkers) != 1 || base.Protected != "Hello "+baseMarkers[0] {
		t.Fatalf("protected payload = %q", base.Protected)
	}
	literal := "MPTK_0123456789ABCDEF_00000000_END"
	source := "Existing " + literal + " remains literal beside %s"
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
	markers := sentinels(one.Protected)
	if len(markers) != 2 || markers[0] != literal || markers[1] == literal {
		t.Fatalf("source collision was not isolated with a scoped namespace: %q", one.Protected)
	}
	if got, err := one.Restore(one.Protected); err != nil || got != source {
		t.Fatalf("collision round trip = %q, %v", got, err)
	}
}

func TestPatchouliQuotedTooltipUsesSafeSentinels(t *testing.T) {
	source := `$(item)Hover: $(t:"minecraft:diamond")Diamond$()$(/t)`
	protected, err := tokenprotect.Protect(source)
	if err != nil {
		t.Fatal(err)
	}
	markers := sentinels(protected.Protected)
	if len(markers) == 0 {
		t.Fatal("expected protected Patchouli markers")
	}
	for _, marker := range markers {
		if strings.ContainsAny(marker, `<>/"'\\`) {
			t.Fatalf("provider-facing marker contains unsafe syntax: %q", marker)
		}
	}
	got, err := protected.Restore(protected.Protected)
	if err != nil || got != source {
		t.Fatalf("Patchouli round trip = %q, %v", got, err)
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
