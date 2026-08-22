package provider

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

func TestProviderProfilesFromEnvDefaultChainUsesOllamaOnly(t *testing.T) {
	env := providerChainEnv("")
	profiles, err := providerProfilesFromEnv(func(name string) string { return env[name] })
	if err != nil || len(profiles) != 1 || profiles[0].Name != "ollama" || profiles[0].BaseURL.String() != "http://localhost:11434/v1" {
		t.Fatalf("profiles=%#v error=%v", profiles, err)
	}
	translator, model, err := buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if got := providerNames(translator); got != "ollama" || model != "qwen3:8b" {
		t.Fatalf("chain=%s model=%s", got, model)
	}
}

func TestProviderProfilesFromEnvCustomProviderParsing(t *testing.T) {
	env := map[string]string{
		"PROVIDER_CHAIN":                "deepinfra,together-ai",
		"PROVIDER_DEEPINFRA_BASE_URL":   "https://api.deepinfra.com/v1/openai/",
		"PROVIDER_DEEPINFRA_API_KEY":    "deep-secret",
		"PROVIDER_DEEPINFRA_MODEL":      "deep-model",
		"PROVIDER_DEEPINFRA_MODE":       "json_schema",
		"PROVIDER_TOGETHER_AI_BASE_URL": "https://api.together.xyz/v1",
		"PROVIDER_TOGETHER_AI_API_KEY":  "together-secret",
		"PROVIDER_TOGETHER_AI_MODEL":    "together-model",
		"PROVIDER_TOGETHER_AI_MODE":     "json_object",
	}
	profiles, err := providerProfilesFromEnv(func(name string) string { return env[name] })
	if err != nil || len(profiles) != 2 {
		t.Fatalf("profiles=%#v error=%v", profiles, err)
	}
	if profiles[0].Name != "deepinfra" || profiles[0].BaseURL.String() != "https://api.deepinfra.com/v1/openai" || profiles[0].Mode != modeJSONSchema {
		t.Fatalf("deepinfra profile=%#v", profiles[0])
	}
	if profiles[1].Name != "together-ai" || profiles[1].Mode != modeJSONObject {
		t.Fatalf("together profile=%#v", profiles[1])
	}
}

func TestBuildTranslatorChainPreservesConfiguredOrder(t *testing.T) {
	env := providerChainEnv("together,deepinfra,ollama")
	translator, model, err := buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if got := providerNames(translator); got != "together,deepinfra,ollama" || model != "qwen3:8b" {
		t.Fatalf("chain=%s model=%s", got, model)
	}
}

func TestProviderProfilesFromEnvMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name, missing, want string
	}{
		{"base URL", "PROVIDER_DEEPINFRA_BASE_URL", "PROVIDER_DEEPINFRA_BASE_URL"},
		{"API key", "PROVIDER_DEEPINFRA_API_KEY", "PROVIDER_DEEPINFRA_API_KEY"},
		{"model", "PROVIDER_DEEPINFRA_MODEL", "PROVIDER_DEEPINFRA_MODEL"},
		{"mode", "PROVIDER_DEEPINFRA_MODE", "PROVIDER_DEEPINFRA_MODE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := providerChainEnv("deepinfra")
			delete(env, tt.missing)
			_, err := providerProfilesFromEnv(func(name string) string { return env[name] })
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "deep-secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestProviderProfilesFromEnvInvalidBaseURLRejected(t *testing.T) {
	for _, invalid := range []string{"http://example.test/v1", "https://user:pass@example.test/v1", "https://example.test/v1?key=secret", "https://example.test/v1#fragment", "https://example.test/v1\n"} {
		t.Run(invalid, func(t *testing.T) {
			env := providerChainEnv("deepinfra")
			env["PROVIDER_DEEPINFRA_BASE_URL"] = invalid
			_, err := providerProfilesFromEnv(func(name string) string { return env[name] })
			if err == nil || strings.Contains(err.Error(), env["PROVIDER_DEEPINFRA_API_KEY"]) {
				t.Fatalf("invalid URL %q error=%v", invalid, err)
			}
		})
	}
}

func TestProviderChainInvalidAndDuplicateNamesRejected(t *testing.T) {
	tests := []struct {
		chain, want string
	}{
		{"deepinfra,,ollama", "empty provider name"},
		{"deepinfra,deepinfra", "duplicate"},
		{"together-ai,together_ai", "duplicate"},
		{"deepinfra.example", "invalid"},
		{"bad\nname", "control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			env := map[string]string{"PROVIDER_CHAIN": tt.chain}
			_, err := providerProfilesFromEnv(func(name string) string { return env[name] })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBuildTranslatorChainIncludesOllamaOnlyWhenPresent(t *testing.T) {
	env := providerChainEnv("deepinfra")
	translator, model, err := buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if got := providerNames(translator); got != "deepinfra" || model != "deep-model" {
		t.Fatalf("chain=%s model=%s", got, model)
	}
	env = providerChainEnv("deepinfra,ollama")
	translator, _, err = buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if got := providerNames(translator); got != "deepinfra,ollama" {
		t.Fatalf("chain=%s", got)
	}
}

func TestProviderChainSummaryIsOrderedAndSecretFree(t *testing.T) {
	env := providerChainEnv("deepinfra,together,ollama")
	delete(env, "PROVIDER_TOGETHER_MODEL")
	env["PROVIDER_OLLAMA_MODEL"] = "local-model"
	summary := providerChainSummary(func(name string) string { return env[name] })
	want := []string{
		"deepinfra: enabled model=deep-model mode=json_schema",
		"together: disabled (PROVIDER_TOGETHER_MODEL not set)",
		"ollama: enabled model=local-model mode=json_schema (final fallback)",
	}
	position := -1
	for _, text := range want {
		next := strings.Index(summary, text)
		if next <= position {
			t.Fatalf("summary order/content=%q, missing %q", summary, text)
		}
		position = next
	}
	for _, secret := range []string{"deep-secret", "together-secret"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("summary leaked secret: %q", summary)
		}
	}
}

func providerChainEnv(chain string) map[string]string {
	return map[string]string{
		"PROVIDER_CHAIN":              chain,
		"PROVIDER_DEEPINFRA_BASE_URL": "https://api.deepinfra.com/v1/openai",
		"PROVIDER_DEEPINFRA_API_KEY":  "deep-secret",
		"PROVIDER_DEEPINFRA_MODEL":    "deep-model",
		"PROVIDER_DEEPINFRA_MODE":     "json_schema",
		"PROVIDER_TOGETHER_BASE_URL":  "https://api.together.xyz/v1",
		"PROVIDER_TOGETHER_API_KEY":   "together-secret",
		"PROVIDER_TOGETHER_MODEL":     "together-model",
		"PROVIDER_TOGETHER_MODE":      "json_object",
		"PROVIDER_OLLAMA_BASE_URL":    "http://localhost:11434/v1",
		"PROVIDER_OLLAMA_MODEL":       "qwen3:8b",
		"PROVIDER_OLLAMA_TIMEOUT":     "10m",
		"PROVIDER_OLLAMA_MODE":        "json_schema",
	}
}

func providerNames(translator Translator) string {
	chain := translator.(*chainTranslator)
	var names []string
	for _, provider := range chain.providers {
		typed, ok := provider.(*openAITranslator)
		if !ok {
			names = append(names, fmt.Sprintf("%T", provider))
			continue
		}
		names = append(names, typed.profile.Name)
	}
	return strings.Join(names, ",")
}

func TestOllamaUsesOpenAICompatibleAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected authorization header %q", auth)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		format := body["response_format"].(map[string]any)
		if body["model"] != "test:8b" || format["type"] != "json_schema" {
			t.Fatalf("body=%#v", body)
		}
		results := format["json_schema"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["results"].(map[string]any)
		if results["minItems"] != float64(1) || results["maxItems"] != float64(1) {
			t.Fatalf("Ollama schema=%#v", results)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"results\":[{\"id\":\"a\",\"translated\":\"Hola\"}]}"}}]}`)
	}))
	defer server.Close()

	env := map[string]string{"PROVIDER_OLLAMA_BASE_URL": server.URL + "/v1", "PROVIDER_OLLAMA_MODEL": "test:8b", "PROVIDER_OLLAMA_TIMEOUT": "3m", "PROVIDER_OLLAMA_MODE": "json_schema"}
	translator, _, err := buildTranslatorChain(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	ollama := translator.(*chainTranslator).providers[0].(*openAITranslator)
	if ollama.client.Timeout != 3*time.Minute {
		t.Fatalf("timeout=%v", ollama.client.Timeout)
	}
	batch, err := translator.Translate(context.Background(), []TranslationRequest{{ID: "a", Source: "Hello"}})
	if err != nil || batch.Identity.Provider != "ollama" || batch.Results[0].Translated != "Hola" {
		t.Fatalf("batch=%#v error=%v", batch, err)
	}
}

func TestOllamaConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"invalid scheme", map[string]string{"PROVIDER_OLLAMA_BASE_URL": "ftp://localhost/v1", "PROVIDER_OLLAMA_MODEL": "model", "PROVIDER_OLLAMA_TIMEOUT": "10m", "PROVIDER_OLLAMA_MODE": "json_schema"}, "PROVIDER_OLLAMA_BASE_URL"},
		{"credentials", map[string]string{"PROVIDER_OLLAMA_BASE_URL": "http://user:pass@localhost/v1", "PROVIDER_OLLAMA_MODEL": "model", "PROVIDER_OLLAMA_TIMEOUT": "10m", "PROVIDER_OLLAMA_MODE": "json_schema"}, "PROVIDER_OLLAMA_BASE_URL"},
		{"query", map[string]string{"PROVIDER_OLLAMA_BASE_URL": "http://localhost/v1?secret=x", "PROVIDER_OLLAMA_MODEL": "model", "PROVIDER_OLLAMA_TIMEOUT": "10m", "PROVIDER_OLLAMA_MODE": "json_schema"}, "PROVIDER_OLLAMA_BASE_URL"},
		{"invalid model", map[string]string{"PROVIDER_OLLAMA_BASE_URL": "http://localhost/v1", "PROVIDER_OLLAMA_MODEL": "bad\nmodel", "PROVIDER_OLLAMA_TIMEOUT": "10m", "PROVIDER_OLLAMA_MODE": "json_schema"}, "control characters"},
		{"invalid timeout", map[string]string{"PROVIDER_OLLAMA_BASE_URL": "http://localhost/v1", "PROVIDER_OLLAMA_MODEL": "model", "PROVIDER_OLLAMA_TIMEOUT": "0s", "PROVIDER_OLLAMA_MODE": "json_schema"}, "PROVIDER_OLLAMA_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := providerProfileFromEnv(newProviderChainEntry("ollama", "OLLAMA"), func(name string) string { return tt.env[name] })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	valid := providerChainEnv("")
	profile, err := providerProfileFromEnv(newProviderChainEntry("ollama", "OLLAMA"), func(name string) string { return valid[name] })
	if err != nil || profile.BaseURL.String() != "http://localhost:11434/v1" || profile.Model != "qwen3:8b" || profile.Timeout != 10*time.Minute || profile.Key != "" {
		t.Fatalf("profile=%#v error=%v", profile, err)
	}

	for _, missing := range []string{"PROVIDER_OLLAMA_BASE_URL", "PROVIDER_OLLAMA_MODEL", "PROVIDER_OLLAMA_TIMEOUT", "PROVIDER_OLLAMA_MODE"} {
		t.Run("missing "+missing, func(t *testing.T) {
			env := providerChainEnv("")
			delete(env, missing)
			_, err := providerProfileFromEnv(newProviderChainEntry("ollama", "OLLAMA"), func(name string) string { return env[name] })
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("error=%v", err)
			}
		})
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
		{"auth advances", 401, `{"error":{"message":"invalid cerebras-key-123"}}`, 1, ""},
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
	if got := output.String(); !strings.Contains(got, "Provider attempt: groq model=m") || !strings.Contains(got, "Provider transition: groq -> ollama kind=quota_exhausted reason=quota_exhausted") || strings.Contains(got, "999999") || strings.Contains(got, "secret") {
		t.Fatalf("safe feedback=%q", got)
	}
}

type scriptedTranslator struct {
	identity ProviderIdentity
	calls    int
	errors   []error
}

func validTranslationResults(requests []TranslationRequest) []TranslationResult {
	results := make([]TranslationResult, len(requests))
	for i, request := range requests {
		results[i] = TranslationResult{ID: request.ID, Translated: request.Source}
	}
	return results
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
	fatal := &scriptedTranslator{errors: []error{errors.New("not a provider error")}}
	unused := &scriptedTranslator{}
	_, err := (&chainTranslator{providers: []Translator{fatal, unused}}).Translate(context.Background(), nil)
	if err == nil || unused.calls != 0 {
		t.Fatalf("fatal error=%v fallback calls=%d", err, unused.calls)
	}
	authNowAdvances := &scriptedTranslator{identity: geminiID, errors: []error{&ProviderError{Identity: geminiID, Kind: ErrorAuth, Reason: "bad key"}}}
	fallback := &scriptedTranslator{identity: ProviderIdentity{Provider: "fallback", Model: "m"}}
	batch, err := (&chainTranslator{providers: []Translator{authNowAdvances, fallback}}).Translate(context.Background(), nil)
	if err != nil || fallback.calls != 1 || batch.Identity.Provider != "fallback" {
		t.Fatalf("batch=%#v error=%v fallback=%d", batch, err, fallback.calls)
	}
}
