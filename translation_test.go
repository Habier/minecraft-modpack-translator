package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modpack-translator/tokenprotect"
)

type fakeTranslator struct {
	calls      [][]TranslationRequest
	fn         func(int, []TranslationRequest) ([]TranslationResult, error)
	maxEntries int
	identity   ProviderIdentity
}

type mixedIdentityTranslator struct{}

func (mixedIdentityTranslator) Plan(requests []TranslationRequest) ([][]TranslationRequest, error) {
	return [][]TranslationRequest{requests}, nil
}

func (mixedIdentityTranslator) Translate(_ context.Context, requests []TranslationRequest) (TranslationBatch, error) {
	results := validTranslationResults(requests)
	for i := range results {
		results[i].Identity = ProviderIdentity{Provider: fmt.Sprintf("provider-%d", i+1), Model: fmt.Sprintf("model-%d", i+1)}
	}
	return TranslationBatch{Results: results, Identity: ProviderIdentity{Provider: "batch", Model: "legacy"}}, nil
}

func (f *fakeTranslator) Plan(requests []TranslationRequest) ([][]TranslationRequest, error) {
	maxEntries := f.maxEntries
	if maxEntries <= 0 {
		maxEntries = len(requests)
	}
	var plans [][]TranslationRequest
	for start := 0; start < len(requests); start += maxEntries {
		end := start + maxEntries
		if end > len(requests) {
			end = len(requests)
		}
		plans = append(plans, append([]TranslationRequest(nil), requests[start:end]...))
	}
	return plans, nil
}

func validTranslationResults(requests []TranslationRequest) []TranslationResult {
	results := make([]TranslationResult, len(requests))
	for i, request := range requests {
		results[i] = TranslationResult{ID: request.ID, Translated: "ES " + request.Source}
	}
	return results
}

func TestValidateTranslationResultsRejectsWrongCount(t *testing.T) {
	requested := map[string]preparedTranslation{"a": {}, "b": {}}
	if _, err := validateTranslationResults([]TranslationResult{{ID: "a"}}, requested); err == nil || !strings.Contains(err.Error(), "1 results for 2") {
		t.Fatalf("count validation error=%v", err)
	}
}

func (f *fakeTranslator) Translate(_ context.Context, requests []TranslationRequest) (TranslationBatch, error) {
	copyRequests := append([]TranslationRequest(nil), requests...)
	f.calls = append(f.calls, copyRequests)
	identity := f.identity
	if identity.Provider == "" {
		identity = ProviderIdentity{Provider: "fake", Model: "test"}
	}
	if f.fn != nil {
		results, err := f.fn(len(f.calls), requests)
		for i := range results {
			results[i].Identity = identity
		}
		return TranslationBatch{Results: results, Identity: identity}, err
	}
	results := validTranslationResults(requests)
	for i := range results {
		results[i].Identity = identity
	}
	return TranslationBatch{Results: results, Identity: identity}, nil
}

func TestTranslateWorkspaceDeduplicatesBatchesCachesAndResumes(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{
		catalogTranslationEntry("id-a", "Hello %s", "assets/a.json"),
		catalogTranslationEntry("id-b", "Hello %s", "assets/b.json"),
		catalogTranslationEntry("id-c", "World", "assets/c.json"),
	})
	provider := &fakeTranslator{maxEntries: 1}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 2 {
		t.Fatalf("provider calls = %d, want 2 deduplicated batches", len(provider.calls))
	}
	for _, call := range provider.calls {
		if len(call) != 1 {
			t.Fatalf("batch size = %d", len(call))
		}
	}
	cache := readTranslationCache(t, translationCachePath(workspace))
	if len(cache.Entries) != 3 {
		t.Fatalf("cache entries = %d", len(cache.Entries))
	}
	if cache.Entries[0].Translation != cache.Entries[1].Translation || !strings.Contains(cache.Entries[0].Translation, "%s") {
		t.Fatalf("fan-out translations = %#v", cache.Entries)
	}
	provider.calls = nil
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("cache rerun calls = %d", len(provider.calls))
	}

	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("id-a", "Changed", "assets/a.json"), catalogTranslationEntry("id-b", "Hello %s", "assets/b.json"), catalogTranslationEntry("id-c", "World", "assets/c.json")})
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 || len(provider.calls[0]) != 1 || provider.calls[0][0].ID != "id-a" {
		t.Fatalf("source invalidation calls = %#v", provider.calls)
	}
	provider.calls = nil
	provider.maxEntries = 0
	cache = readTranslationCache(t, translationCachePath(workspace))
	cache.PromptVersion = "en-target-minecraft-v1"
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(translationCachePath(workspace), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 || len(provider.calls[0]) != 3 {
		t.Fatalf("prompt invalidation calls = %#v", provider.calls)
	}
	cache = readTranslationCache(t, translationCachePath(workspace))
	if cache.PromptVersion != translationPromptV2 {
		t.Fatalf("prompt version = %q, want %q", cache.PromptVersion, translationPromptV2)
	}
	provider.calls = nil
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("matching v2 cache was not reused: %#v", provider.calls)
	}
	otherModel := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, otherModel, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(otherModel.calls) != 0 {
		t.Fatalf("provenance-only model change invalidated semantic cache: %#v", otherModel.calls)
	}
}

func TestTranslateWorkspaceUsesDeterministicContiguousProviderPlans(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("c", strings.Repeat("C", 40), "c"), catalogTranslationEntry("a", strings.Repeat("A", 40), "a"), catalogTranslationEntry("b", strings.Repeat("B", 40), "b")})
	provider := &fakeTranslator{maxEntries: 1}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("byte-split calls = %d", len(provider.calls))
	}
	firstOrder := []string{provider.calls[0][0].ID, provider.calls[1][0].ID, provider.calls[2][0].ID}
	secondWorkspace := t.TempDir()
	writeTranslationCatalog(t, secondWorkspace, []CatalogEntryV1{catalogTranslationEntry("c", strings.Repeat("C", 40), "c"), catalogTranslationEntry("a", strings.Repeat("A", 40), "a"), catalogTranslationEntry("b", strings.Repeat("B", 40), "b")})
	second := &fakeTranslator{maxEntries: 1}
	if err := translateWorkspace(context.Background(), secondWorkspace, second, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	secondOrder := []string{second.calls[0][0].ID, second.calls[1][0].ID, second.calls[2][0].ID}
	if strings.Join(firstOrder, ",") != strings.Join(secondOrder, ",") {
		t.Fatalf("nondeterministic order: %v != %v", firstOrder, secondOrder)
	}
}

func TestTranslateWorkspaceOrdersContextAndKeepsCatalogOutputOrder(t *testing.T) {
	workspace := t.TempDir()
	entries := []CatalogEntryV1{
		catalogTranslationEntry("duplicate-first", "Same", "assets/zeta/lang/z.json"),
		catalogTranslationEntry("beta-2", "Beta two", "assets/beta/lang/shared.json"),
		catalogTranslationEntry("alpha-1", "Alpha one", "assets/alpha/lang/shared.json"),
		catalogTranslationEntry("duplicate-later", "Same", "assets/alpha/lang/a.json"),
		catalogTranslationEntry("beta-1", "Beta one", `assets\beta\lang\shared.json`),
		catalogTranslationEntry("alpha-2", "Alpha two", "assets/alpha/lang/shared.json"),
	}
	writeTranslationCatalog(t, workspace, entries)
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("calls=%#v", provider.calls)
	}
	requests := provider.calls[0]
	wantIDs := []string{"alpha-1", "alpha-2", "beta-2", "beta-1", "duplicate-first"}
	if len(requests) != len(wantIDs) {
		t.Fatalf("requests=%#v", requests)
	}
	for i, want := range wantIDs {
		if requests[i].ID != want {
			t.Fatalf("request order=%#v want=%#v", requests, wantIDs)
		}
	}
	if requests[3].SourceFile != "assets/beta/lang/shared.json" {
		t.Fatalf("normalized source file=%q", requests[3].SourceFile)
	}
	if requests[len(requests)-1].SourceFile != "assets/zeta/lang/z.json" {
		t.Fatalf("duplicate representative=%#v", requests[len(requests)-1])
	}
	cache := readTranslationCache(t, translationCachePath(workspace))
	if len(cache.Entries) != len(entries) {
		t.Fatalf("cache=%#v", cache.Entries)
	}
	for i, entry := range entries {
		if cache.Entries[i].ID != entry.ID {
			t.Fatalf("cache order=%#v", cache.Entries)
		}
	}
	if cache.Entries[0].Translation != cache.Entries[3].Translation {
		t.Fatalf("duplicate fan-out=%#v", cache.Entries)
	}
}

func TestNormalizeSourceFileTreatsSlashStylesIdentically(t *testing.T) {
	inputs := []string{
		"assets/beta/lang/shared.json",
		`assets\beta\lang\shared.json`,
		`assets/beta\lang/./shared.json`,
	}
	for _, input := range inputs {
		if got := normalizeSourceFile(input); got != "assets/beta/lang/shared.json" {
			t.Errorf("normalizeSourceFile(%q) = %q", input, got)
		}
	}
}

func TestTranslateWorkspaceRetriesInvalidResponseBeforeSplitting(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	provider := &fakeTranslator{fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 1 {
			return nil, &invalidTranslationResponseError{err: errors.New("decode Ollama structured translation: malformed JSON")}
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 2 || len(readTranslationCache(t, translationCachePath(workspace)).Entries) != 2 {
		t.Fatalf("calls=%d cache=%#v", len(provider.calls), readTranslationCache(t, translationCachePath(workspace)).Entries)
	}
}

func TestTranslateWorkspacePrintsValidatedProviderIdentity(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	output, err := captureStdout(t, func() error {
		return translateWorkspace(context.Background(), workspace, &fakeTranslator{}, translationOptions{})
	})
	if err != nil || !strings.Contains(output, "Validated batch: provider=fake model=test entries=1") {
		t.Fatalf("output=%q error=%v", output, err)
	}
}

func TestTranslateWorkspaceCachesPerResultProviderIdentity(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	if err := translateWorkspace(context.Background(), workspace, mixedIdentityTranslator{}, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := readTranslationCache(t, translationCachePath(workspace)).Entries
	if len(entries) != 2 || entries[0].Provider != "provider-1" || entries[0].Model != "model-1" || entries[1].Provider != "provider-2" || entries[1].Model != "model-2" {
		t.Fatalf("cache provenance=%#v", entries)
	}
}

func TestTranslateWorkspacePublishesPartialResultsAndRetriesOnlyUnfinished(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	translator := &fakeTranslator{fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 1 {
			if got := requestIDs(requests); got != "a,b" {
				t.Fatalf("first request IDs=%s", got)
			}
			return validTranslationResults(requests[:1]), &invalidTranslationResponseError{err: errors.New("invalid later child")}
		}
		if got := requestIDs(requests); got != "b" {
			t.Fatalf("retry request IDs=%s", got)
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, translator, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := readTranslationCache(t, translationCachePath(workspace)).Entries
	if len(entries) != 2 || entries[0].Provider != "fake" || entries[0].Model != "test" || len(translator.calls) != 2 {
		t.Fatalf("calls=%#v cache=%#v", translator.calls, entries)
	}
}

func TestTranslateWorkspaceDoesNotPublishInvalidPartialResult(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "&6Two&r", "b")})
	translator := &fakeTranslator{fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 1 {
			return []TranslationResult{{ID: requests[0].ID, Translated: requests[0].Source}, {ID: requests[1].ID, Translated: "missing markers"}}, &invalidTranslationResponseError{err: errors.New("invalid later child")}
		}
		if got := requestIDs(requests); got != "b" {
			t.Fatalf("retry request IDs=%s", got)
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, translator, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := readTranslationCache(t, translationCachePath(workspace)).Entries
	if len(entries) != 2 || !strings.Contains(entries[1].Translation, "&6Two&r") || entries[1].Translation == "missing markers" || len(translator.calls) != 2 {
		t.Fatalf("calls=%#v cache=%#v", translator.calls, entries)
	}
}

func TestTranslateWorkspaceTerminatesWhenResponseAnomalyFollowsCompleteResults(t *testing.T) {
	for _, tt := range []struct {
		name string
		fn   func([]TranslationRequest) ([]TranslationResult, error)
	}{
		{
			name: "unknown extra output",
			fn: func(requests []TranslationRequest) ([]TranslationResult, error) {
				results := validTranslationResults(requests)
				return append(results, TranslationResult{ID: "unknown", Translated: "extra"}), nil
			},
		},
		{
			name: "accompanying invalid response",
			fn: func(requests []TranslationRequest) ([]TranslationResult, error) {
				return validTranslationResults(requests), &invalidTranslationResponseError{err: errors.New("residual invalid response")}
			},
		},
		{
			name: "repeated invalid response cannot reach empty retry",
			fn: func(requests []TranslationRequest) ([]TranslationResult, error) {
				return validTranslationResults(requests), &invalidTranslationResponseError{err: errors.New("would repeat forever")}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
			emptyCalls := 0
			translator := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
				if len(requests) == 0 {
					emptyCalls++
					return nil, &invalidTranslationResponseError{err: errors.New("empty translation call")}
				}
				return tt.fn(requests)
			}}
			if err := translateWorkspace(context.Background(), workspace, translator, translationOptions{}); err != nil {
				t.Fatal(err)
			}
			entries := readTranslationCache(t, translationCachePath(workspace)).Entries
			if len(translator.calls) != 1 || emptyCalls != 0 || len(entries) != 2 {
				t.Fatalf("calls=%#v empty=%d cache=%#v", translator.calls, emptyCalls, entries)
			}
		})
	}
}

func TestTranslateWorkspaceRetriesOnlyConflictingDuplicateID(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	translator := &fakeTranslator{fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 1 {
			return []TranslationResult{
				{ID: requests[0].ID, Translated: requests[0].Source},
				{ID: requests[1].ID, Translated: "conflict-first"},
				{ID: requests[1].ID, Translated: "conflict-second"},
			}, nil
		}
		if got := requestIDs(requests); got != "b" {
			t.Fatalf("retry request IDs=%s", got)
		}
		entries := readTranslationCache(t, translationCachePath(workspace)).Entries
		if len(entries) != 1 || entries[0].ID != "a" {
			t.Fatalf("cache before duplicate retry=%#v", entries)
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, translator, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := readTranslationCache(t, translationCachePath(workspace)).Entries
	if len(translator.calls) != 2 || requestIDs(translator.calls[0]) != "a,b" || requestIDs(translator.calls[1]) != "b" || len(entries) != 2 || entries[1].Translation == "conflict-first" || entries[1].Translation == "conflict-second" {
		t.Fatalf("calls=%#v cache=%#v", translator.calls, entries)
	}
}

func TestTranslateWorkspaceCachesPartialResultsBeforeProviderExhaustion(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	identity := ProviderIdentity{Provider: "first", Model: "model-a"}
	exhausted := &fakeTranslator{identity: identity, fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		return validTranslationResults(requests[:1]), &ProviderError{Identity: ProviderIdentity{Provider: "translation chain"}, Kind: ErrorQuota, Reason: "configured providers exhausted"}
	}}
	if err := translateWorkspace(context.Background(), workspace, exhausted, translationOptions{}); err == nil {
		t.Fatal("provider exhaustion unexpectedly succeeded")
	}
	entries := readTranslationCache(t, translationCachePath(workspace)).Entries
	if len(entries) != 1 || entries[0].ID != "a" || entries[0].Provider != identity.Provider || entries[0].Model != identity.Model {
		t.Fatalf("partial cache=%#v wanted identity=%#v", entries, identity)
	}
	resume := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		if got := requestIDs(requests); got != "b" {
			t.Fatalf("resume request IDs=%s", got)
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, resume, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(resume.calls) != 1 || len(readTranslationCache(t, translationCachePath(workspace)).Entries) != 2 {
		t.Fatalf("resume calls=%#v", resume.calls)
	}
}

func requestIDs(requests []TranslationRequest) string {
	ids := make([]string, len(requests))
	for i, request := range requests {
		ids[i] = request.ID
	}
	return strings.Join(ids, ",")
}

func TestTranslateWorkspaceRejectsDroppedAmpersandFormattingMarker(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "&6Controller&r", "a")})
	provider := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		return []TranslationResult{{ID: requests[0].ID, Translated: "ES Controller"}}, nil
	}}

	err := translateWorkspace(context.Background(), workspace, provider, translationOptions{})
	var partial *TranslationPartialError
	if !errors.As(err, &partial) || partial.Successful != 0 || partial.Failed != 1 {
		t.Fatalf("partial error = %#v (%v)", partial, err)
	}
	if len(provider.calls) != validationRetries+1 {
		t.Fatalf("provider calls = %d, want %d", len(provider.calls), validationRetries+1)
	}
	if entries := readTranslationCache(t, translationCachePath(workspace)).Entries; len(entries) != 0 {
		t.Fatalf("invalid translation cached = %#v", entries)
	}
	var report translationFailureReport
	if err := json.Unmarshal(mustRead(t, partial.ReportPath), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Reason, "missing marker") {
		t.Fatalf("failure report = %#v", report.Failures)
	}
}

func TestTranslateWorkspaceSplitsSalvagesContinuesAndRerunsFailures(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b"), catalogTranslationEntry("c", "Three", "c")})
	failedID := ""
	provider := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		if failedID == "" {
			failedID = requests[0].ID
		}
		if len(requests) > 1 || requests[0].ID == failedID {
			return []TranslationResult{{ID: "unknown", Translated: "bad"}}, nil
		}
		return validTranslationResults(requests), nil
	}}
	err := translateWorkspace(context.Background(), workspace, provider, translationOptions{})
	var partial *TranslationPartialError
	if !errors.As(err, &partial) || partial.Successful != 2 || partial.Cached != 0 || partial.Failed != 1 {
		t.Fatalf("partial error = %#v (%v)", partial, err)
	}
	cache := readTranslationCache(t, translationCachePath(workspace))
	if len(cache.Entries) != 2 {
		t.Fatalf("salvaged cache entries = %#v", cache.Entries)
	}
	seenSuccessAfterFailure := false
	seenFailedSingleton := false
	for _, call := range provider.calls {
		if len(call) == 1 && call[0].ID == failedID {
			seenFailedSingleton = true
		} else if seenFailedSingleton && len(call) == 1 {
			seenSuccessAfterFailure = true
		}
	}
	if !seenSuccessAfterFailure {
		t.Fatalf("later work did not continue after failed singleton: %#v", provider.calls)
	}
	reportPath := translationFailureReportPath(workspace)
	firstReport := mustRead(t, reportPath)
	rerun := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		return []TranslationResult{{ID: "unknown", Translated: "bad"}}, nil
	}}
	err = translateWorkspace(context.Background(), workspace, rerun, translationOptions{})
	if !errors.As(err, &partial) || len(rerun.calls) != 2 || len(rerun.calls[0]) != 1 || rerun.calls[0][0].ID != failedID {
		t.Fatalf("rerun error=%v calls=%#v", err, rerun.calls)
	}
	if secondReport := mustRead(t, reportPath); string(firstReport) != string(secondReport) {
		t.Fatalf("failure report changed across rerun\nfirst: %s\nsecond: %s", firstReport, secondReport)
	}
	recovery := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, recovery, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(recovery.calls) != 1 || len(recovery.calls[0]) != 1 || recovery.calls[0][0].ID != failedID {
		t.Fatalf("recovery calls = %#v", recovery.calls)
	}
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("stale failure report remains: %v", err)
	}
}

func TestTranslateWorkspaceReportsDuplicateFanOutAsCatalogEntries(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("duplicate-a", "Same", "a"), catalogTranslationEntry("duplicate-b", "Same", "b"), catalogTranslationEntry("good", "Good", "c")})
	provider := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		if len(requests) > 1 || requests[0].Source == "Same" {
			return []TranslationResult{{ID: "unknown", Translated: "bad"}}, nil
		}
		return validTranslationResults(requests), nil
	}}
	err := translateWorkspace(context.Background(), workspace, provider, translationOptions{})
	var partial *TranslationPartialError
	if !errors.As(err, &partial) || partial.Successful != 1 || partial.Failed != 2 {
		t.Fatalf("partial error = %#v (%v)", partial, err)
	}
	var report translationFailureReport
	if err := json.Unmarshal(mustRead(t, partial.ReportPath), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Failures) != 2 || report.Failures[0].ID != "duplicate-a" || report.Failures[1].ID != "duplicate-b" {
		t.Fatalf("duplicate failures = %#v", report.Failures)
	}
	if entries := readTranslationCache(t, translationCachePath(workspace)).Entries; len(entries) != 1 || entries[0].ID != "good" {
		t.Fatalf("salvaged cache = %#v", entries)
	}
}

func TestTranslateWorkspacePreservesPartialBatchesAndPreviousCache(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	provider := &fakeTranslator{maxEntries: 1, fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 2 {
			return nil, errors.New("interrupted")
		}
		return []TranslationResult{{ID: requests[0].ID, Translated: "ES " + requests[0].Source}}, nil
	}}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err == nil {
		t.Fatal("interruption error = nil")
	}
	before := mustRead(t, translationCachePath(workspace))
	if len(readTranslationCache(t, translationCachePath(workspace)).Entries) != 1 {
		t.Fatal("first batch not published")
	}
	if err := os.WriteFile(translationCachePath(workspace), []byte(`{"bad":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := translateWorkspace(context.Background(), workspace, &fakeTranslator{}, translationOptions{}); err == nil {
		t.Fatal("malformed cache accepted")
	}
	if got := string(mustRead(t, translationCachePath(workspace))); got != `{"bad":true}` {
		t.Fatal("malformed cache overwritten")
	}
	if err := os.WriteFile(translationCachePath(workspace), before, 0644); err != nil {
		t.Fatal(err)
	}
	resume := &fakeTranslator{maxEntries: 1}
	if err := translateWorkspace(context.Background(), workspace, resume, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(resume.calls) != 1 || resume.calls[0][0].ID == readTranslationCache(t, translationCachePath(workspace)).Entries[0].ID {
		t.Fatalf("resume calls = %#v", resume.calls)
	}
}

func TestTranslateWorkspaceDoesNotPublishStaleEntriesUnderNewPromptVersion(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{
		catalogTranslationEntry("a", "One", "a"),
		catalogTranslationEntry("b", "Two", "b"),
		catalogTranslationEntry("c", "Three", "c"),
	})
	if err := translateWorkspace(context.Background(), workspace, &fakeTranslator{}, translationOptions{}); err != nil {
		t.Fatal(err)
	}

	cachePath := translationCachePath(workspace)
	cache := readTranslationCache(t, cachePath)
	cache.PromptVersion = "en-target-minecraft-v1"
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	interrupted := &fakeTranslator{maxEntries: 1, fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 2 {
			return nil, errors.New("interrupted")
		}
		return validTranslationResults(requests), nil
	}}
	if err := translateWorkspace(context.Background(), workspace, interrupted, translationOptions{}); err == nil {
		t.Fatal("interruption error = nil")
	}
	if len(interrupted.calls) != 2 {
		t.Fatalf("interrupted calls = %#v", interrupted.calls)
	}
	completedID := interrupted.calls[0][0].ID
	published := readTranslationCache(t, cachePath)
	if published.PromptVersion != translationPromptV2 || len(published.Entries) != 1 || published.Entries[0].ID != completedID {
		t.Fatalf("incrementally published cache = %#v", published)
	}

	retry := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, retry, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(retry.calls) != 1 || len(retry.calls[0]) != 2 {
		t.Fatalf("retry calls = %#v", retry.calls)
	}
	requested := map[string]bool{}
	for _, request := range retry.calls[0] {
		requested[request.ID] = true
	}
	if requested[completedID] || len(requested) != 2 {
		t.Fatalf("retry reused stale entries or missed v2 progress: completed=%q requested=%#v", completedID, requested)
	}
}

func TestTranslationCacheRecoversPreviousAndPublishesEmpty(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	path := translationCachePath(workspace)
	if err := translateWorkspace(context.Background(), workspace, &fakeTranslator{}, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("recovery translated %d batches", len(provider.calls))
	}
	writeTranslationCatalog(t, workspace, nil)
	if err := translateWorkspace(context.Background(), workspace, provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if entries := readTranslationCache(t, path).Entries; len(entries) != 0 || entries == nil {
		t.Fatalf("empty entries = %#v", entries)
	}
}

func TestTranslationCachePathUsesLocaleSpecificFilesForNonDefaultTargets(t *testing.T) {
	workspace := t.TempDir()
	if got := translationCachePath(workspace, "es_es"); got != filepath.Join(workspace, "translations", "translations.v2.json") {
		t.Fatalf("default cache path = %q", got)
	}
	if got := translationCachePath(workspace, "fr_fr"); got != filepath.Join(workspace, "translations", "translations.fr_fr.v2.json") {
		t.Fatalf("fr cache path = %q", got)
	}
	if got := translationFailureReportPath(workspace, "fr_fr"); got != filepath.Join(workspace, "translations", "failures.fr_fr.v2.json") {
		t.Fatalf("fr failure path = %q", got)
	}
}

func TestLoadCatalogAbsentMalformedAndExportUntouched(t *testing.T) {
	workspace := t.TempDir()
	if _, err := loadCatalog(filepath.Join(workspace, "catalog", "catalog.v1.json")); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("absent error = %v", err)
	}
	writeFiles(t, "", map[string][]byte{filepath.Join(workspace, "catalog", "catalog.v1.json"): []byte(`{"schema":"wrong"}`)})
	if _, err := loadCatalog(filepath.Join(workspace, "catalog", "catalog.v1.json")); err == nil {
		t.Fatal("malformed catalog accepted")
	}
	root := t.TempDir()
	workspace, export := outputPaths(root)
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	if err := translateWorkspace(context.Background(), workspace, &fakeTranslator{}, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(export, "translations")); !os.IsNotExist(err) {
		t.Fatalf("translation files under export: %v", err)
	}
}

func catalogTranslationEntry(id, source, file string) CatalogEntryV1 {
	tokens := tokenprotect.Find(source)
	catalogTokens := make([]CatalogTokenV1, len(tokens))
	for i, token := range tokens {
		catalogTokens[i] = CatalogTokenV1{Kind: string(token.Kind), Text: token.Text, Start: token.Start, End: token.End}
	}
	return CatalogEntryV1{ID: id, SourceKind: "standard_lang", SourceFile: file, Locator: "/key", Source: source, TargetLocale: targetLanguageCode, Tokens: catalogTokens, Writeback: CatalogWritebackV1{Format: "json", ValueType: "string", Container: "object", SourceSHA256: strings.Repeat("0", 64), Encoding: "utf-8"}}
}

func writeTranslationCatalog(t *testing.T, workspace string, entries []CatalogEntryV1) {
	t.Helper()
	catalog := CatalogV1{Schema: catalogSchema, SourceLocale: sourceLanguageCode, TargetLocale: targetLanguageCode, Entries: entries}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, "", map[string][]byte{filepath.Join(workspace, "catalog", "catalog.v1.json"): data})
}

func readTranslationCache(t *testing.T, path string) TranslationCacheV2 {
	t.Helper()
	var cache TranslationCacheV2
	if err := json.Unmarshal(mustRead(t, path), &cache); err != nil {
		t.Fatal(err)
	}
	return cache
}
