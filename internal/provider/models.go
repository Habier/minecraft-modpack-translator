package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// ModelDiagnostic contains the safe model-listing result for one configured provider.
type ModelDiagnostic struct {
	Provider        string
	ConfiguredModel string
	Models          []string
	Err             error
}

// ListModels queries every configured provider in chain order.
func ListModels(ctx context.Context, getenv func(string) string) ([]ModelDiagnostic, error) {
	entries, err := providerChainFromEnv(getenv)
	if err != nil {
		return nil, err
	}

	results := make([]ModelDiagnostic, 0, len(entries))
	failed := 0
	for _, entry := range entries {
		result := ModelDiagnostic{Provider: entry.name}
		profile, profileErr := providerProfileFromEnv(entry, getenv)
		if profileErr != nil {
			result.Err = profileErr
		} else {
			result.ConfiguredModel = profile.Model
			result.Models, result.Err = listProviderModels(ctx, profile)
		}
		if result.Err != nil {
			failed++
		}
		results = append(results, result)
	}
	if failed > 0 {
		return results, fmt.Errorf("model diagnostics failed for %d of %d providers", failed, len(results))
	}
	return results, nil
}

func listProviderModels(ctx context.Context, profile providerProfile) ([]string, error) {
	identity := ProviderIdentity{Provider: profile.Name, Model: profile.Model}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.BaseURL.String()+"/models", nil)
	if err != nil {
		return nil, &ProviderError{Identity: identity, Kind: ErrorConfig, Reason: "could not construct request"}
	}
	if profile.Key != "" {
		request.Header.Set("Authorization", "Bearer "+profile.Key)
	}
	timeout := profile.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		kind := ErrorUnavailable
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			kind = ErrorCancelled
		}
		return nil, &ProviderError{Identity: identity, Kind: kind, Reason: "request to " + profile.BaseURL.Redacted() + " failed"}
	}
	defer response.Body.Close()
	body, err := readLimitedBody(response.Body, maxCloudResponseBody)
	if err != nil {
		return nil, &ProviderError{Identity: identity, Kind: ErrorUnavailable, Reason: "response exceeded the safe size limit or could not be read"}
	}
	if response.StatusCode != http.StatusOK {
		return nil, classifyProviderResponse(profile.Name, identity, response.StatusCode, body)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &ProviderError{Identity: identity, Kind: ErrorUnavailable, Reason: "provider returned a malformed models response"}
	}
	models := make([]string, 0, len(envelope.Data))
	for _, model := range envelope.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}
