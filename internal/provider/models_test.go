package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModelsContinuesAfterProviderFailure(t *testing.T) {
	queried := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queried = true
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model"},{"id":"configured-model"}]}`)
	}))
	defer server.Close()

	env := map[string]string{
		"PROVIDER_CHAIN":           "broken,ollama",
		"PROVIDER_OLLAMA_BASE_URL": server.URL + "/v1",
		"PROVIDER_OLLAMA_MODEL":    "configured-model",
		"PROVIDER_OLLAMA_TIMEOUT":  "1s",
		"PROVIDER_OLLAMA_MODE":     "json_schema",
		"PROVIDER_BROKEN_API_KEY":  "do-not-print",
		"PROVIDER_BROKEN_MODEL":    "broken-model",
		"PROVIDER_BROKEN_MODE":     "json_schema",
	}
	results, err := ListModels(context.Background(), func(name string) string { return env[name] })
	if err == nil || err.Error() != "model diagnostics failed for 1 of 2 providers" {
		t.Fatalf("error = %v", err)
	}
	if !queried {
		t.Fatal("later provider was not queried")
	}
	if len(results) != 2 || results[0].Provider != "broken" || results[0].Err == nil || results[1].Provider != "ollama" {
		t.Fatalf("results = %#v", results)
	}
	if got := strings.Join(results[1].Models, ","); got != "configured-model,z-model" {
		t.Fatalf("models = %q", got)
	}
	if strings.Contains(results[0].Err.Error(), env["PROVIDER_BROKEN_API_KEY"]) {
		t.Fatalf("error exposed API key: %v", results[0].Err)
	}
}

func TestListProviderModelsUsesAuthorizationAndReportsSafeHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantModels string
		wantError  string
	}{
		{name: "success", status: http.StatusOK, body: `{"data":[{"id":"b"},{"id":"a"}]}`, wantModels: "a,b"},
		{name: "HTTP failure", status: http.StatusUnauthorized, body: `{"error":{"message":"secret response body"}}`, wantError: "test provider (authentication) failed: HTTP 401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer api-secret" {
					t.Errorf("Authorization = %q", got)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			profile := providerProfile{Name: "test", Key: "api-secret", Model: "configured"}
			profile.BaseURL, _ = parseProviderURL("TEST_URL", server.URL, true)
			models, err := listProviderModels(context.Background(), profile)
			if strings.Join(models, ",") != tt.wantModels {
				t.Fatalf("models = %v", models)
			}
			if tt.wantError == "" && err != nil || tt.wantError != "" && (err == nil || err.Error() != tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "secret response body") {
				t.Fatalf("error exposed response body: %v", err)
			}
		})
	}
}
