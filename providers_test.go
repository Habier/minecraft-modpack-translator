package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProviderProfilesFromEnvOrderAndValidation(t *testing.T) {
	env := map[string]string{
		"GEMINI_API_KEY": "gem-secret", "GEMINI_MODEL": "gem-model",
		"CEREBRAS_API_KEY": "cer-secret", "CEREBRAS_MODEL": "cer-model",
		"GROQ_API_KEY": "groq-secret", "GROQ_MODEL": "groq-model",
		"MISTRAL_API_KEY": "mis-secret", "MISTRAL_MODEL": "mis-model",
		"OPENROUTER_API_KEY": "or-secret", "OPENROUTER_MODEL": "or-model",
	}
	profiles, err := providerProfilesFromEnv(func(name string) string { return env[name] })
	if err != nil || len(profiles) != 5 {
		t.Fatalf("profiles=%#v error=%v", profiles, err)
	}
	var names []string
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	if got := strings.Join(names, ","); got != "gemini,cerebras,groq,mistral,openrouter" {
		t.Fatalf("provider order=%s", got)
	}
	for _, invalid := range []string{"http://example.test/v1", "https://user:pass@example.test/v1", "https://example.test/v1?key=secret", "https://example.test/v1#fragment"} {
		env["GEMINI_BASE_URL"] = invalid
		_, err := providerProfilesFromEnv(func(name string) string { return env[name] })
		if err == nil || strings.Contains(err.Error(), "gem-secret") {
			t.Fatalf("invalid URL %q error=%v", invalid, err)
		}
	}
	delete(env, "GEMINI_BASE_URL")
	delete(env, "GEMINI_MODEL")
	if _, err := providerProfilesFromEnv(func(name string) string { return env[name] }); err == nil || !strings.Contains(err.Error(), "GEMINI_MODEL") {
		t.Fatalf("missing model error=%v", err)
	}
	delete(env, "GEMINI_API_KEY")
	delete(env, "CEREBRAS_MODEL")
	if _, err := providerProfilesFromEnv(func(name string) string { return env[name] }); err == nil || !strings.Contains(err.Error(), "CEREBRAS_MODEL") {
		t.Fatalf("missing Cerebras model error=%v", err)
	}
}

func TestCerebrasRequiresOfficialHostWithoutLeakingKey(t *testing.T) {
	env := map[string]string{"CEREBRAS_API_KEY": "cer-secret", "CEREBRAS_MODEL": "model", "CEREBRAS_BASE_URL": "https://api.cerebras.ai/v1"}
	if profiles, err := providerProfilesFromEnv(func(name string) string { return env[name] }); err != nil || len(profiles) != 1 {
		t.Fatalf("official host profiles=%#v error=%v", profiles, err)
	}
	env["CEREBRAS_BASE_URL"] = "https://attacker.example/v1"
	_, err := providerProfilesFromEnv(func(name string) string { return env[name] })
	if err == nil || strings.Contains(err.Error(), env["CEREBRAS_API_KEY"]) {
		t.Fatalf("arbitrary host error=%v", err)
	}
}

func TestBuildTranslatorChainExactOrder(t *testing.T) {
	env := map[string]string{
		"GEMINI_API_KEY": "g", "GEMINI_MODEL": "gm",
		"CEREBRAS_API_KEY": "c", "CEREBRAS_MODEL": "cm",
		"GROQ_API_KEY": "q", "GROQ_MODEL": "qm",
		"MISTRAL_API_KEY": "m", "MISTRAL_MODEL": "mm",
		"OPENROUTER_API_KEY": "o", "OPENROUTER_MODEL": "om",
	}
	translator, _, err := buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	chain := translator.(*chainTranslator)
	var names []string
	for _, provider := range chain.providers {
		switch typed := provider.(type) {
		case *openAITranslator:
			names = append(names, typed.profile.Name)
		case *ollamaTranslator:
			names = append(names, "ollama")
		default:
			t.Fatalf("unexpected provider %T", provider)
		}
	}
	if got := strings.Join(names, ","); got != "gemini,cerebras,groq,mistral,openrouter,ollama" {
		t.Fatalf("provider order=%s", got)
	}
}

func TestProviderChainSummaryIsOrderedAndSecretFree(t *testing.T) {
	env := map[string]string{
		"GEMINI_API_KEY": "gem-secret", "GEMINI_MODEL": "gem-model",
		"CEREBRAS_API_KEY": "cer-secret", "CEREBRAS_MODEL": "qwen-3-32b",
		"OLLAMA_MODEL": "local-model",
	}
	summary := providerChainSummary(func(name string) string { return env[name] })
	want := []string{
		"gemini: enabled model=gem-model",
		"cerebras: enabled model=qwen-3-32b",
		"groq: disabled (GROQ_API_KEY not set)",
		"mistral: disabled (MISTRAL_API_KEY not set)",
		"openrouter: disabled (OPENROUTER_API_KEY not set)",
		"ollama: enabled model=local-model (final fallback)",
	}
	position := -1
	for _, text := range want {
		next := strings.Index(summary, text)
		if next <= position {
			t.Fatalf("summary order/content=%q, missing %q", summary, text)
		}
		position = next
	}
	for _, secret := range []string{"gem-secret", "cer-secret"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("summary leaked secret: %q", summary)
		}
	}
}

func TestProviderChainSummaryAllCloudsAbsent(t *testing.T) {
	summary := providerChainSummary(func(string) string { return "" })
	if strings.Count(summary, "disabled") != len(cloudDefinitions) || !strings.Contains(summary, "ollama: enabled model="+defaultOllamaModel) {
		t.Fatalf("summary=%q", summary)
	}
}

func TestCerebrasAdapterContractAndIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer cerebras-secret" {
			t.Errorf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		format := body["response_format"].(map[string]any)
		schema := format["json_schema"].(map[string]any)
		if body["model"] != "qwen-3-32b" || format["type"] != "json_schema" || schema["strict"] != true {
			t.Fatalf("body=%#v", body)
		}
		results := schema["schema"].(map[string]any)["properties"].(map[string]any)["results"].(map[string]any)
		if _, min := results["minItems"]; min || results["maxItems"] != nil {
			t.Fatalf("Cerebras schema has unsupported array constraints: %#v", results)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"results\":[{\"id\":\"a\",\"translated\":\"Hola\"}]}"}}]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/v1")
	translator := newOpenAITranslator(providerProfile{Name: "cerebras", Key: "cerebras-secret", Model: "qwen-3-32b", BaseURL: base, Mode: modeJSONSchema})
	batch, err := translator.Translate(context.Background(), []TranslationRequest{{ID: "a", Source: "Hello"}})
	if err != nil || batch.Identity != (ProviderIdentity{Provider: "cerebras", Model: "qwen-3-32b"}) || batch.Results[0].Translated != "Hola" {
		t.Fatalf("batch=%#v error=%v", batch, err)
	}
}

func TestOpenAIAdapterContractAndStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["response_format"].(map[string]any)["type"] != "json_schema" {
			t.Fatalf("body=%#v", body)
		}
		io.WriteString(w, `{"id":"x","model":"actual","choices":[{"message":{"role":"assistant","content":"{\"results\":[{\"id\":\"a\",\"translated\":\"Hola\"}]}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/v1")
	translator := newOpenAITranslator(providerProfile{Name: "gemini", Key: "secret", Model: "configured", BaseURL: base, Mode: modeJSONSchema})
	batch, err := translator.Translate(context.Background(), []TranslationRequest{{ID: "a", Source: "Hello"}})
	if err != nil || batch.Identity.Provider != "gemini" || batch.Identity.Model != "configured" || batch.Results[0].Translated != "Hola" {
		t.Fatalf("batch=%#v error=%v", batch, err)
	}
}

func TestProviderClassifiers(t *testing.T) {
	tests := []struct {
		provider string
		status   int
		body     string
		kind     TranslationErrorKind
	}{
		{"gemini", 429, `{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`, ErrorQuota},
		{"gemini", 429, `{"error":{"message":"unknown throttle"}}`, ErrorQuota},
		{"cerebras", 429, `{"error":{"type":"rate_limit_exceeded","message":"Rate limit exceeded"}}`, ErrorQuota},
		{"cerebras", 429, `{"error":{"message":"quota exhausted"}}`, ErrorQuota},
		{"cerebras", 401, `{"error":{"message":"secret body"}}`, ErrorAuth},
		{"groq", 429, `{"error":{"code":"rate_limit_exceeded","message":"tokens per minute"}}`, ErrorQuota},
		{"mistral", 429, `{"message":"rate limit exceeded"}`, ErrorQuota},
		{"openrouter", 429, `{"error":{"message":"insufficient credits"}}`, ErrorQuota},
		{"openrouter", 402, `{"error":{"message":"insufficient credits"}}`, ErrorQuota},
		{"groq", 401, `{}`, ErrorAuth}, {"groq", 403, `{}`, ErrorPermission}, {"groq", 400, `{}`, ErrorRequest}, {"groq", 404, `{}`, ErrorModel}, {"groq", 503, `{}`, ErrorUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.provider+tt.body, func(t *testing.T) {
			err := classifyProviderResponse(tt.provider, ProviderIdentity{Provider: tt.provider, Model: "m"}, tt.status, []byte(tt.body))
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Kind != tt.kind || strings.Contains(err.Error(), tt.body) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCerebrasQuotaFallsBackAndAuthStopsWithoutSecrets(t *testing.T) {
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		wantCalls int
		wantKind  TranslationErrorKind
	}{
		{"quota advances", 429, `{"error":{"type":"rate_limit_exceeded","message":"limit for cerebras-key-123"}}`, 1, ""},
		{"auth stops", 401, `{"error":{"message":"invalid cerebras-key-123"}}`, 0, ErrorAuth},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			cloud := newOpenAITranslator(providerProfile{Name: "cerebras", Key: "cerebras-key-123", Model: "qwen-3-32b", BaseURL: base, Mode: modeJSONSchema})
			cloud.maxRetries = 0
			fallback := &scriptedTranslator{identity: ProviderIdentity{Provider: "groq", Model: "g"}}
			batch, err := (&chainTranslator{providers: []Translator{cloud, fallback}}).Translate(context.Background(), nil)
			if strings.Contains(fmt.Sprint(err), "cerebras-key-123") || strings.Contains(fmt.Sprint(err), tt.body) {
				t.Fatalf("secret or response body leaked: %v", err)
			}
			if fallback.calls != tt.wantCalls {
				t.Fatalf("fallback calls=%d error=%v batch=%#v", fallback.calls, err, batch)
			}
			if tt.wantKind != "" {
				var providerErr *ProviderError
				if !errors.As(err, &providerErr) || providerErr.Kind != tt.wantKind {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil || batch.Identity.Provider != "groq" {
				t.Fatalf("batch=%#v error=%v", batch, err)
			}
		})
	}
}

func TestOpenAIAdapterRetriesRetryAfter(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"results\":[]}"}}]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	translator := newOpenAITranslator(providerProfile{Name: "groq", Key: "secret", Model: "m", BaseURL: base, Mode: modeJSONObject})
	var delays []time.Duration
	translator.sleep = func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }
	_, err := translator.Translate(context.Background(), nil)
	if err != nil || calls != 2 || len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("calls=%d delays=%v error=%v", calls, delays, err)
	}
}

func TestRetryDelayBoundsServerValues(t *testing.T) {
	tests := []struct {
		name, value string
		want        time.Duration
	}{
		{"normal seconds", "2", 2 * time.Second},
		{"huge seconds", "999999999999999999", maxServerRetryDelay},
		{"huge date", time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat), maxServerRetryDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryDelay(tt.value, 200*time.Millisecond); got != tt.want {
				t.Fatalf("retryDelay(%q)=%v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCerebrasRetryHeadersAreSafeAndBounded(t *testing.T) {
	header := http.Header{"X-Ratelimit-Reset-Tokens-Minute": []string{"1.25s"}, "X-Ratelimit-Reset-Requests-Day": []string{"999999999"}}
	if got := providerRetryDelay("cerebras", header, 200*time.Millisecond); got != 1250*time.Millisecond {
		t.Fatalf("delay=%v", got)
	}
	header.Set("Retry-After", "999999999999999999")
	if got := providerRetryDelay("cerebras", header, 200*time.Millisecond); got != maxServerRetryDelay {
		t.Fatalf("bounded delay=%v", got)
	}
	header = http.Header{"X-Ratelimit-Reset-Tokens-Minute": []string{"NaN"}}
	if got := providerRetryDelay("cerebras", header, 200*time.Millisecond); got != 200*time.Millisecond {
		t.Fatalf("malformed delay=%v", got)
	}
}

func TestCerebrasResetDelayForms(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{{"1.25s", 1250 * time.Millisecond, true}, {"1.25", 1250 * time.Millisecond, true}, {"999999999s", maxServerRetryDelay, true}, {"-1s", 0, false}, {"NaN", 0, false}, {"1e999", 0, false}}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got, ok := cerebrasResetDelay(tt.value); got != tt.want || ok != tt.ok {
				t.Fatalf("delay=%v ok=%v, want %v %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCerebrasMalformedAndOversizeResponsesAreRejected(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"malformed", `{"choices":[]}`},
		{"oversize", strings.Repeat("x", maxCloudResponseBody+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, tt.body) }))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			translator := newOpenAITranslator(providerProfile{Name: "cerebras", Key: "secret", Model: "qwen-3-32b", BaseURL: base, Mode: modeJSONSchema})
			batch, err := translator.Translate(context.Background(), nil)
			if err == nil || batch.Identity.Provider != "cerebras" && tt.name == "malformed" {
				t.Fatalf("batch=%#v error=%v", batch, err)
			}
		})
	}
}

func TestBoundedRetryReachesFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "999999999999999999")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	cloud := newOpenAITranslator(providerProfile{Name: "groq", Key: "secret", Model: "m", BaseURL: base, Mode: modeJSONObject})
	var delay time.Duration
	cloud.maxRetries = 1
	cloud.sleep = func(ctx context.Context, d time.Duration) error { delay = d; return sleepContext(ctx, 0) }
	fallback := &scriptedTranslator{identity: ProviderIdentity{Provider: "ollama", Model: "local"}}
	var output strings.Builder
	batch, err := (&chainTranslator{providers: []Translator{cloud, fallback}, output: &output}).Translate(context.Background(), nil)
	if err != nil || batch.Identity.Provider != "ollama" || delay != maxServerRetryDelay || fallback.calls != 1 {
		t.Fatalf("batch=%#v delay=%v fallback calls=%d error=%v", batch, delay, fallback.calls, err)
	}
	if got := output.String(); !strings.Contains(got, "Provider attempt: groq model=m") || !strings.Contains(got, "Provider transition: groq -> ollama reason=rate or quota limit") || strings.Contains(got, "999999") || strings.Contains(got, "secret") {
		t.Fatalf("safe feedback=%q", got)
	}
}

type scriptedTranslator struct {
	identity ProviderIdentity
	calls    int
	errors   []error
}

func (s *scriptedTranslator) ProviderIdentity() ProviderIdentity { return s.identity }

func (s *scriptedTranslator) Translate(_ context.Context, requests []TranslationRequest) (TranslationBatch, error) {
	s.calls++
	if len(s.errors) >= s.calls && s.errors[s.calls-1] != nil {
		return TranslationBatch{}, s.errors[s.calls-1]
	}
	return TranslationBatch{Results: validTranslationResults(requests), Identity: s.identity}, nil
}

func TestChainAdvancesPermanentlyOnlyForQuota(t *testing.T) {
	geminiID := ProviderIdentity{Provider: "gemini", Model: "g"}
	gemini := &scriptedTranslator{identity: geminiID, errors: []error{&ProviderError{Identity: geminiID, Kind: ErrorQuota, Reason: "quota"}}}
	groq := &scriptedTranslator{identity: ProviderIdentity{Provider: "groq", Model: "q"}}
	chain := &chainTranslator{providers: []Translator{gemini, groq}}
	for i := 0; i < 2; i++ {
		batch, err := chain.Translate(context.Background(), []TranslationRequest{{ID: "a", Source: "x"}})
		if err != nil || batch.Identity.Provider != "groq" {
			t.Fatalf("batch=%#v error=%v", batch, err)
		}
	}
	if gemini.calls != 1 || groq.calls != 2 {
		t.Fatalf("calls gemini=%d groq=%d", gemini.calls, groq.calls)
	}
	fatal := &scriptedTranslator{errors: []error{&ProviderError{Identity: geminiID, Kind: ErrorAuth, Reason: "bad key"}}}
	unused := &scriptedTranslator{}
	_, err := (&chainTranslator{providers: []Translator{fatal, unused}}).Translate(context.Background(), nil)
	if err == nil || unused.calls != 0 {
		t.Fatalf("fatal error=%v fallback calls=%d", err, unused.calls)
	}
}
