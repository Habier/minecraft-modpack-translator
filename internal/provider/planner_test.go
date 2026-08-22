package provider

import (
	"net/url"
	"strings"
	"testing"
)

func plannerForTest(limits Limits) *openAITranslator {
	base, _ := url.Parse("https://example.test/v1")
	return newOpenAITranslator(providerProfile{Name: "test", Model: "model", BaseURL: base, Mode: modeJSONSchema, Limits: limits})
}

func TestEstimateSerializedRequestUsesConservativeUTF8Bytes(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "minimal"},
		{name: "ASCII", source: "plain text"},
		{name: "multibyte UTF-8", source: "矿石 áéí"},
		{name: "JSON escaping", source: "quote: \" slash: \\ newline:\n"},
		{name: "marker-heavy", source: strings.Repeat("__MPT_0123456789abcdef_000001__", 8)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := plannerForTest(Limits{})
			body, err := translator.requestBody([]TranslationRequest{{ID: "id", Source: tt.source, TargetLocale: "es_es"}})
			if err != nil {
				t.Fatal(err)
			}
			got := estimateRequest(body)
			if got.Bytes != len(body) || got.InputTokens != (len(body)+2)/3 {
				t.Fatalf("estimate=%#v bytes=%d", got, len(body))
			}
		})
	}
}

func TestPlannerBoundariesAndOversizedSingletons(t *testing.T) {
	request := TranslationRequest{ID: "safe-id", Source: "hello", TargetLocale: "es_es"}
	tokenProbe := plannerForTest(Limits{MaxOutputTokens: 7})
	tokenBody, err := tokenProbe.requestBody([]TranslationRequest{request})
	if err != nil {
		t.Fatal(err)
	}
	tokenEstimate := estimateRequest(tokenBody)
	byteProbe := plannerForTest(Limits{MaxOutputTokens: 1})
	byteBody, err := byteProbe.requestBody([]TranslationRequest{request})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		limits      Limits
		wantError   string
		wantBatches int
	}{
		{name: "exact token boundary", limits: Limits{ContextTokens: tokenEstimate.InputTokens + 7, MaxOutputTokens: 7, MaxRequestBytes: len(tokenBody), MaxEntries: 1}, wantBatches: 1},
		{name: "one over token boundary", limits: Limits{ContextTokens: tokenEstimate.InputTokens + 6, MaxOutputTokens: 7, MaxRequestBytes: len(tokenBody) + 100, MaxEntries: 1}, wantError: "safe-id exceeds provider context token limit"},
		{name: "exact request byte boundary", limits: Limits{ContextTokens: 1 << 20, MaxOutputTokens: 1, MaxRequestBytes: len(byteBody), MaxEntries: 1}, wantBatches: 1},
		{name: "one over request byte boundary", limits: Limits{ContextTokens: 1 << 20, MaxOutputTokens: 1, MaxRequestBytes: len(byteBody) - 1, MaxEntries: 1}, wantError: "safe-id exceeds provider request byte limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plans, err := plannerForTest(tt.limits).Plan([]TranslationRequest{request})
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || strings.Contains(err.Error(), request.Source) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || len(plans) != tt.wantBatches {
				t.Fatalf("plans=%#v error=%v", plans, err)
			}
		})
	}
}

func TestPlannerGreedilyBuildsDeterministicContiguousPlans(t *testing.T) {
	requests := []TranslationRequest{{ID: "a", Source: "A"}, {ID: "b", Source: "B"}, {ID: "c", Source: "C"}, {ID: "d", Source: "D"}, {ID: "e", Source: "E"}}
	translator := plannerForTest(Limits{ContextTokens: 1 << 20, MaxOutputTokens: 1, MaxRequestBytes: 1 << 20, MaxEntries: 2})
	want := []string{"a,b", "c,d", "e"}
	for run := 0; run < 2; run++ {
		plans, err := translator.Plan(requests)
		if err != nil || len(plans) != len(want) {
			t.Fatalf("plans=%#v error=%v", plans, err)
		}
		for i, plan := range plans {
			ids := make([]string, len(plan))
			for j := range plan {
				ids[j] = plan[j].ID
			}
			if got := strings.Join(ids, ","); got != want[i] {
				t.Fatalf("plan %d=%q want=%q", i, got, want[i])
			}
		}
	}
	if plans, err := translator.Plan(nil); err != nil || len(plans) != 0 {
		t.Fatalf("empty plans=%#v error=%v", plans, err)
	}
}
