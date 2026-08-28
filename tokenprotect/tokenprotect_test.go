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
	first, second := `<keep id="0"/>`, `<keep id="1"/>`
	tests := []struct {
		name, translated, problem string
	}{
		{"missing", "Valor " + first, "missing placeholder"},
		{"duplicated", "Valor " + first + " " + first + " " + second, "duplicated placeholder"},
		{"unknown", "Valor " + first + ` <keep id="999"/>`, "unknown placeholder"},
		{"malformed", "Valor " + first + ` <keep id="1">`, "malformed placeholder"},
		{"escaped", "Valor " + first + ` &lt;keep id="1"/&gt;`, "escaped placeholder"},
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

func TestRestoreIgnoresBenignPlaceholderTagPrefixes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		prefix func(string) string
	}{
		{
			name:   "default tag raw and escaped prefixes",
			source: "Value %s",
			prefix: func(string) string { return `<keeper>safe</keeper> &lt;keepalive <keep id="0"/>` },
		},
		{
			name:   "scoped tag raw and escaped prefixes",
			source: `Literal <keep id="9"/> and %s`,
			prefix: func(protected string) string {
				start := strings.Index(protected, "<keep-")
				end := strings.Index(protected[start:], " ")
				tag := protected[start+1 : start+end]
				return "<" + tag + `alive>safe</` + tag + `alive> &lt;` + tag + `keeper ` + protected[start:start+strings.Index(protected[start:], ">")+1]
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protected, err := tokenprotect.Protect(tt.source)
			if err != nil {
				t.Fatal(err)
			}
			translated := tt.prefix(protected.Protected)
			if _, err := protected.Restore(translated); err != nil {
				t.Fatalf("Restore(%q) rejected benign tag prefix text: %v", translated, err)
			}
		})
	}
}

func TestRepeatedIdenticalTokensHaveDistinctMarkers(t *testing.T) {
	protected, err := tokenprotect.Protect("%1$s and %1$s")
	if err != nil {
		t.Fatal(err)
	}
	first, second := `<keep id="0"/>`, `<keep id="1"/>`
	if !strings.Contains(protected.Protected, first) || !strings.Contains(protected.Protected, second) {
		t.Fatal("repeated occurrences must have distinct markers")
	}
	if _, err := protected.Restore(second + " y " + first); err == nil {
		t.Fatal("repeated placeholders must retain relative order")
	}
}

func TestPlaceholderTagIsShortAndAvoidsSourceCollision(t *testing.T) {
	base, err := tokenprotect.Protect("Hello %s")
	if err != nil {
		t.Fatal(err)
	}
	if base.Protected != `Hello <keep id="0"/>` {
		t.Fatalf("default protected payload = %q", base.Protected)
	}
	source := `Existing <keep id="0"/> and &lt;keep id="1"/&gt; remain literal beside %s`
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
	if !strings.Contains(one.Protected, `<keep id="0"/>`) || !strings.Contains(one.Protected, `&lt;keep id="1"/&gt;`) || !strings.Contains(one.Protected, `<keep-`) {
		t.Fatalf("source collision was not isolated with a scoped tag: %q", one.Protected)
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
