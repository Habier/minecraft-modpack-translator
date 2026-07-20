package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tnze/go-mc/nbt"
)

func TestNormalizeFTBQuestSNBT(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "root list", in: `[{a:1} {b:2}]`, want: `[{a:1} ,{b:2}]`},
		{name: "nested list", in: "{x:[[ {a:1}\n{b:2}\t{c:3}, {d:4} ]]}", want: "{x:[[ {a:1}\n,{b:2}\t,{c:3}, {d:4} ]]}"},
		{name: "mixed separators", in: `[{a:1}, {b:2}  {c:3}]`, want: `[{a:1}, {b:2}  ,{c:3}]`},
		{name: "escaped strings", in: `[{text:"escaped \"} { text",other:'} { also text'} {title:"next"}]`, want: `[{text:"escaped \"} { text",other:'} { also text'} ,{title:"next"}]`},
		{name: "string list", in: `["first" "second"]`, want: `["first" ,"second"]`},
		{name: "multiple string list elements", in: `['first'
"second"  'third']`, want: `['first'
,"second"  ,'third']`},
		{name: "nested string lists", in: `{groups:[["one" "two"],['three' 'four']]}`, want: `{groups:[["one" ,"two"],['three' ,'four']]}`},
		{name: "escaped string list elements", in: `["quote: \" and slash: \\" "next" 'single: \' and slash: \\' 'last']`, want: `["quote: \" and slash: \\" ,"next" ,'single: \' and slash: \\' ,'last']`},
		{name: "structural characters inside string list", in: `["{key:value} #tag" '] bracket: colon: #hash']`, want: `["{key:value} #tag" ,'] bracket: colon: #hash']`},
		{name: "mixed string separators", in: `["one", 'two' "three", "four"]`, want: `["one", 'two' ,"three", "four"]`},
		{name: "compound values", in: `{left:{a:1},right:{b:2}}`, want: `{left:{a:1},right:{b:2}}`},
		{name: "compound quoted string", in: "{title:\"one\"\nsubtitle : 'two'}", want: "{title:\"one\"\n,subtitle : 'two'}"},
		{name: "compound numeric values", in: `{integer:1 float:2.5 typed:3b next:4L}`, want: `{integer:1 ,float:2.5 ,typed:3b ,next:4L}`},
		{name: "compound bare values", in: `{enabled:true state:available tag:#forge:ingots item:minecraft:stone}`, want: `{enabled:true ,state:available ,tag:#forge:ingots ,item:minecraft:stone}`},
		{name: "compound collection values", in: `{items:[1,2] nested:{value:1} "final key" :"done"}`, want: `{items:[1,2] ,nested:{value:1} ,"final key" :"done"}`},
		{name: "compound typed array value", in: `{bytes:[B;1b,2b] next:3}`, want: `{bytes:[B;1b,2b] ,next:3}`},
		{name: "nested omitted members", in: "{outer:{a:1\n b:[{x:true y:false} {x:false y:true}] c:{d:4 e:5}} end:6}", want: "{outer:{a:1\n ,b:[{x:true ,y:false} ,{x:false ,y:true}] ,c:{d:4 ,e:5}} ,end:6}"},
		{name: "mixed compound separators", in: `{a:1, b:2 c:3, d:4}`, want: `{a:1, b:2 ,c:3, d:4}`},
		{name: "string resembling key", in: `{text:"not a key: still text" next:"value"}`, want: `{text:"not a key: still text" ,next:"value"}`},
		{name: "typed arrays", in: `{bytes:[B;1b,2b],ints:[I;1,2],longs:[L;1l,2l]}`, want: `{bytes:[B;1b,2b],ints:[I;1,2],longs:[L;1l,2l]}`},
		{name: "real-like byte array", in: `[B; 0b 1b 2b 3b 4b 5b 6b 7b 8b 9b 10b 11b 12b 13b 14b 15b]`, want: `[B; 0b ,1b ,2b ,3b ,4b ,5b ,6b ,7b ,8b ,9b ,10b ,11b ,12b ,13b ,14b ,15b]`},
		{name: "typed array signs and suffix case", in: `{bytes:[B;-128b 0B 127b],ints:[I;-2147483648 0 2147483647],longs:[L;-9223372036854775808l 0L 9223372036854775807l]}`, want: `{bytes:[B;-128b ,0B ,127b],ints:[I;-2147483648 ,0 ,2147483647],longs:[L;-9223372036854775808l ,0L ,9223372036854775807l]}`},
		{name: "mixed typed array separators", in: `{bytes:[B;1b, 2B 3b,4B]}`, want: `{bytes:[B;1b, 2B ,3b,4B]}`},
		{name: "standard unchanged", in: `{items:[{a:1}, {b:2}],name:"standard"}`, want: `{items:[{a:1}, {b:2}],name:"standard"}`},
		{name: "standard typed arrays unchanged", in: `{bytes:[B;-1b, 0B, 1b],ints:[I;-1,0,1],longs:[L;-1l,0L,1l]}`, want: `{bytes:[B;-1b, 0B, 1b],ints:[I;-1,0,1],longs:[L;-1l,0L,1l]}`},
		{name: "standard string list unchanged", in: `{labels:["one", 'two'],name:"standard"}`, want: `{labels:["one", 'two'],name:"standard"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeFTBQuestSNBT([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("normalized = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeFTBQuestSNBTRejectsMalformedInputWithLocation(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "mismatched delimiter", in: "{items:[{a:1}\n{b:2}}"},
		{name: "unterminated string", in: `{title:"missing}`},
		{name: "unterminated escape", in: `{title:"missing\`},
		{name: "unterminated list string", in: `{labels:["first" "missing]}`},
		{name: "comment", in: "{items:[{a:1} // no\n{b:2}]}"},
		{name: "unknown separator", in: `{items:[{a:1};{b:2}]}`},
		{name: "compound value followed by non-key", in: `{left:{a:1} {b:2}}`},
		{name: "bare value followed by ambiguous text", in: `{value:one two}`},
		{name: "bare list values remain ambiguous", in: `{values:[one two]}`},
		{name: "numeric list values remain ambiguous", in: `{values:[1 2]}`},
		{name: "byte array wrong suffix", in: `{values:[B;1b 2]}`},
		{name: "int array wrong suffix", in: `{values:[I;1 2l]}`},
		{name: "long array wrong suffix", in: `{values:[L;1l 2]}`},
		{name: "typed array malformed number", in: `{values:[B;1b --2b]}`},
		{name: "typed array out of range", in: `{values:[B;1b 128b]}`},
		{name: "quoted lookahead without colon", in: `{value:one "two"}`},
		{name: "missing next value", in: `{value:one next:}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeFTBQuestSNBT([]byte(tt.in))
			if err == nil {
				t.Fatal("normalizeFTBQuestSNBT() error = nil")
			}
			for _, part := range []string{"byte ", "line ", "column "} {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("error %q does not contain %q", err, part)
				}
			}
		})
	}
}

func TestNormalizedTypedArraysParseWithTnze(t *testing.T) {
	data := []byte(`{bytes:[B;-128b 0B 127b],ints:[I;-2147483648 0 2147483647],longs:[L;-9223372036854775808l 0L 9223372036854775807l]}`)
	normalized, err := normalizeFTBQuestSNBT(data)
	if err != nil {
		t.Fatal(err)
	}
	var binaryNBT bytes.Buffer
	if err := nbt.StringifiedMessage(normalized).MarshalNBT(&binaryNBT); err != nil {
		t.Fatalf("Tnze rejected normalized typed arrays: %v", err)
	}
}

func TestFTBQuestDialectCatalogPreservesOriginalBytesAndHash(t *testing.T) {
	workspace := t.TempDir()
	source := []byte("{title:\"Group file\",bytes:[B;0b 1B],labels:[\"one\" \"two\"],chapter_groups:[\n\t{id:\"1\",title:\"First group\"}\n\t{id:\"2\",title:\"Second group\"}\n]}")
	file := filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests", "chapter_groups", "chapter_groups.snbt")
	writeFiles(t, "", map[string][]byte{file: source})

	_, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, file); !bytes.Equal(got, source) {
		t.Fatal("catalog parsing changed the original FTB quest source")
	}
	wantHash := sha256.Sum256(source)
	entries := readCatalog(t, catalogPath).Entries
	if len(entries) != 1 || entries[0].Source != "Group file" {
		t.Fatalf("catalog entries = %#v", entries)
	}
	if entries[0].Writeback.SourceSHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("source hash = %s, want original hash", entries[0].Writeback.SourceSHA256)
	}
}

func TestFTBQuestDialectRepresentativeFilesCatalogAllowlistedFields(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests")
	writeFiles(t, "", map[string][]byte{
		filepath.Join(root, "chapter_groups", "groups.snbt"): []byte(`{title:"Groups",chapter_groups:[{id:"1"} {id:"2"}]}`),
		filepath.Join(root, "chapters", "chapter.snbt"):      []byte("{title:\"Chapter\"\nsubtitle:[\"Chapter sub\"] bytes:[B;0b 1B 2b 3B 4b 5B 6b 7B 8b 9B 10b 11B 12b 13B 14b 15B] images:[{hover:\"First hover\"} {hover:\"Second hover\"}] quests:[{id:\"1\" enabled:true subtitle:\"Nested quest subtitle\"} {id:\"2\" enabled:false}]}"),
		filepath.Join(root, "quests", "quest.snbt"):          []byte(`{description:["First line" 'Second line' "Third: {#line}"]}`),
		filepath.Join(root, "reward_tables", "rewards.snbt"): []byte(`{title:"Rewards",rewards:[{title:"Hidden nested"} {type:"item"}]}`),
	})
	_, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, entry := range readCatalog(t, catalogPath).Entries {
		got[entry.Source] = true
	}
	for _, want := range []string{"Groups", "Chapter", "Chapter sub", "First line", "Second line", "Third: {#line}", "First hover", "Second hover", "Rewards"} {
		if !got[want] {
			t.Errorf("catalog is missing %q", want)
		}
	}
	if got["Hidden nested"] {
		t.Error("catalog included a non-allowlisted nested reward-table field")
	}
}

func TestFTBChapterSubtitleListsAndLocalizationReferences(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests")
	writeFiles(t, "", map[string][]byte{
		filepath.Join(root, "chapters", "chapter.snbt"): []byte(`{title:"{player}",subtitle:["Chapter prose","{ftbquests.chapter.example.subtitle0}","Talk to {player}","{other.reference}","   "],quests:[{subtitle:"{ftbquests.quest.example.subtitle}"},{subtitle:"Nested prose"}]}`),
		filepath.Join(root, "quests", "quest.snbt"):     []byte(`{subtitle:"Quest prose",description:["{ftbquests.quest.example.description}","Mixed {ftbquests.quest.example.description}"]}`),
		filepath.Join(root, "lang", "en_us.json5"):      []byte(`{title:{'0123456789abcdef':'{ftbquests.quest.example.title}'},quest_subtitle:{'fedcba9876543210':'JSON5 {player}'}}`),
	})

	_, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	entries := readCatalog(t, catalogPath).Entries
	bySource := map[string]CatalogEntryV1{}
	for _, entry := range entries {
		bySource[entry.Source] = entry
	}
	for _, want := range []string{"{player}", "Chapter prose", "Talk to {player}", "{other.reference}", "Quest prose", "Mixed {ftbquests.quest.example.description}", "JSON5 {player}"} {
		if _, ok := bySource[want]; !ok {
			t.Errorf("catalog is missing %q", want)
		}
	}
	for _, excluded := range []string{"{ftbquests.chapter.example.subtitle0}", "{ftbquests.quest.example.description}", "{ftbquests.quest.example.title}", "Nested prose"} {
		if _, ok := bySource[excluded]; ok {
			t.Errorf("catalog contains excluded %q", excluded)
		}
	}
	entry := bySource["Chapter prose"]
	if entry.Locator != "/subtitle/0" || entry.Writeback.ValueType != "string" || entry.Writeback.Container != "array" || entry.Writeback.ArrayIndex == nil || *entry.Writeback.ArrayIndex != 0 {
		t.Fatalf("chapter subtitle metadata = %#v", entry)
	}
}

func TestFTBChapterSubtitleRejectsNonStringListsAndPreservesCatalog(t *testing.T) {
	workspace := t.TempDir()
	valid := filepath.Join(workspace, "assets", "example", "lang", pendingTranslationFileName())
	chapter := filepath.Join(workspace, "sources", "ftbquests", "config", "ftbquests", "quests", "chapters", "chapter.snbt")
	writeFiles(t, "", map[string][]byte{valid: []byte(`{"key":"previous"}`)})
	_, catalogPath, err := buildCatalog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	previous := mustRead(t, catalogPath)

	for _, source := range []string{`{subtitle:["valid",1]}`, `{subtitle:[1,2]}`} {
		if err := os.MkdirAll(filepath.Dir(chapter), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(chapter, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := buildCatalog(workspace); err == nil || !strings.Contains(err.Error(), "chapters/chapter.snbt") {
			t.Fatalf("buildCatalog() error = %v", err)
		}
		if got := mustRead(t, catalogPath); !bytes.Equal(got, previous) {
			t.Fatal("failed FTB build changed previous catalog")
		}
	}
}
