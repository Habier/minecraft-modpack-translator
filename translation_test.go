package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modpack-translator/tokenprotect"
)

type fakeTranslator struct {
	calls [][]TranslationRequest
	fn    func(int, []TranslationRequest) ([]TranslationResult, error)
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
	if f.fn != nil {
		results, err := f.fn(len(f.calls), requests)
		return TranslationBatch{Results: results, Identity: ProviderIdentity{Provider: "fake", Model: "test"}}, err
	}
	return TranslationBatch{Results: validTranslationResults(requests), Identity: ProviderIdentity{Provider: "fake", Model: "test"}}, nil
}

func TestTranslateWorkspaceDeduplicatesBatchesCachesAndResumes(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{
		catalogTranslationEntry("id-a", "Hello %s", "assets/a.json"),
		catalogTranslationEntry("id-b", "Hello %s", "assets/b.json"),
		catalogTranslationEntry("id-c", "World", "assets/c.json"),
	})
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model:1", provider, translationOptions{BatchSize: 1, BatchBytes: 4096}); err != nil {
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
	cache := readTranslationCache(t, translationCachePath(workspace, "model:1"))
	if len(cache.Entries) != 3 {
		t.Fatalf("cache entries = %d", len(cache.Entries))
	}
	if cache.Entries[0].Translation != cache.Entries[1].Translation || !strings.Contains(cache.Entries[0].Translation, "%s") {
		t.Fatalf("fan-out translations = %#v", cache.Entries)
	}
	provider.calls = nil
	if err := translateWorkspace(context.Background(), workspace, "model:1", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("cache rerun calls = %d", len(provider.calls))
	}

	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("id-a", "Changed", "assets/a.json"), catalogTranslationEntry("id-b", "Hello %s", "assets/b.json"), catalogTranslationEntry("id-c", "World", "assets/c.json")})
	if err := translateWorkspace(context.Background(), workspace, "model:1", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 || len(provider.calls[0]) != 1 || provider.calls[0][0].ID != "id-a" {
		t.Fatalf("source invalidation calls = %#v", provider.calls)
	}
	provider.calls = nil
	cache = readTranslationCache(t, translationCachePath(workspace, "model:1"))
	cache.PromptVersion = "old-prompt"
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(translationCachePath(workspace, "model:1"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := translateWorkspace(context.Background(), workspace, "model:1", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 || len(provider.calls[0]) != 3 {
		t.Fatalf("prompt invalidation calls = %#v", provider.calls)
	}
	otherModel := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model:2", otherModel, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(otherModel.calls) != 0 {
		t.Fatalf("provenance-only model change invalidated semantic cache: %#v", otherModel.calls)
	}
}

func TestTranslateWorkspaceSplitsByBytesDeterministically(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("c", strings.Repeat("C", 40), "c"), catalogTranslationEntry("a", strings.Repeat("A", 40), "a"), catalogTranslationEntry("b", strings.Repeat("B", 40), "b")})
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{BatchSize: 20, BatchBytes: 340}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("byte-split calls = %d", len(provider.calls))
	}
	firstOrder := []string{provider.calls[0][0].ID, provider.calls[1][0].ID, provider.calls[2][0].ID}
	secondWorkspace := t.TempDir()
	writeTranslationCatalog(t, secondWorkspace, []CatalogEntryV1{catalogTranslationEntry("c", strings.Repeat("C", 40), "c"), catalogTranslationEntry("a", strings.Repeat("A", 40), "a"), catalogTranslationEntry("b", strings.Repeat("B", 40), "b")})
	second := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), secondWorkspace, "model", second, translationOptions{BatchSize: 20, BatchBytes: 340}); err != nil {
		t.Fatal(err)
	}
	secondOrder := []string{second.calls[0][0].ID, second.calls[1][0].ID, second.calls[2][0].ID}
	if strings.Join(firstOrder, ",") != strings.Join(secondOrder, ",") {
		t.Fatalf("nondeterministic order: %v != %v", firstOrder, secondOrder)
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
	if err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 2 || len(readTranslationCache(t, translationCachePath(workspace, "model")).Entries) != 2 {
		t.Fatalf("calls=%d cache=%#v", len(provider.calls), readTranslationCache(t, translationCachePath(workspace, "model")).Entries)
	}
}

func TestTranslateWorkspacePrintsValidatedProviderIdentity(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	output, err := captureStdout(t, func() error {
		return translateWorkspace(context.Background(), workspace, "model", &fakeTranslator{}, translationOptions{})
	})
	if err != nil || !strings.Contains(output, "Validated batch: provider=fake model=test entries=1") {
		t.Fatalf("output=%q error=%v", output, err)
	}
}

func TestTranslateWorkspaceRejectsDroppedAmpersandFormattingMarker(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "&6Controller&r", "a")})
	provider := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		return []TranslationResult{{ID: requests[0].ID, Translated: "ES Controller"}}, nil
	}}

	err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{})
	var partial *TranslationPartialError
	if !errors.As(err, &partial) || partial.Successful != 0 || partial.Failed != 1 {
		t.Fatalf("partial error = %#v (%v)", partial, err)
	}
	if len(provider.calls) != validationRetries+1 {
		t.Fatalf("provider calls = %d, want %d", len(provider.calls), validationRetries+1)
	}
	if entries := readTranslationCache(t, translationCachePath(workspace, "model")).Entries; len(entries) != 0 {
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
	err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{})
	var partial *TranslationPartialError
	if !errors.As(err, &partial) || partial.Successful != 2 || partial.Cached != 0 || partial.Failed != 1 {
		t.Fatalf("partial error = %#v (%v)", partial, err)
	}
	cache := readTranslationCache(t, translationCachePath(workspace, "model"))
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
	reportPath := translationFailureReportPath(workspace, "model")
	firstReport := mustRead(t, reportPath)
	rerun := &fakeTranslator{fn: func(_ int, requests []TranslationRequest) ([]TranslationResult, error) {
		return []TranslationResult{{ID: "unknown", Translated: "bad"}}, nil
	}}
	err = translateWorkspace(context.Background(), workspace, "model", rerun, translationOptions{})
	if !errors.As(err, &partial) || len(rerun.calls) != 2 || len(rerun.calls[0]) != 1 || rerun.calls[0][0].ID != failedID {
		t.Fatalf("rerun error=%v calls=%#v", err, rerun.calls)
	}
	if secondReport := mustRead(t, reportPath); string(firstReport) != string(secondReport) {
		t.Fatalf("failure report changed across rerun\nfirst: %s\nsecond: %s", firstReport, secondReport)
	}
	recovery := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model", recovery, translationOptions{}); err != nil {
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
	err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{})
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
	if entries := readTranslationCache(t, translationCachePath(workspace, "model")).Entries; len(entries) != 1 || entries[0].ID != "good" {
		t.Fatalf("salvaged cache = %#v", entries)
	}
}

func TestTranslateWorkspacePreservesPartialBatchesAndPreviousCache(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a"), catalogTranslationEntry("b", "Two", "b")})
	provider := &fakeTranslator{fn: func(call int, requests []TranslationRequest) ([]TranslationResult, error) {
		if call == 2 {
			return nil, errors.New("interrupted")
		}
		return []TranslationResult{{ID: requests[0].ID, Translated: "ES " + requests[0].Source}}, nil
	}}
	if err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{BatchSize: 1}); err == nil {
		t.Fatal("interruption error = nil")
	}
	before := mustRead(t, translationCachePath(workspace, "model"))
	if len(readTranslationCache(t, translationCachePath(workspace, "model")).Entries) != 1 {
		t.Fatal("first batch not published")
	}
	if err := os.WriteFile(translationCachePath(workspace, "model"), []byte(`{"bad":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := translateWorkspace(context.Background(), workspace, "model", &fakeTranslator{}, translationOptions{}); err == nil {
		t.Fatal("malformed cache accepted")
	}
	if got := string(mustRead(t, translationCachePath(workspace, "model"))); got != `{"bad":true}` {
		t.Fatal("malformed cache overwritten")
	}
	if err := os.WriteFile(translationCachePath(workspace, "model"), before, 0644); err != nil {
		t.Fatal(err)
	}
	resume := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model", resume, translationOptions{BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	if len(resume.calls) != 1 || resume.calls[0][0].ID == readTranslationCache(t, translationCachePath(workspace, "model")).Entries[0].ID {
		t.Fatalf("resume calls = %#v", resume.calls)
	}
}

func TestTranslationCacheRecoversPreviousAndPublishesEmpty(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	path := translationCachePath(workspace, "model")
	if err := translateWorkspace(context.Background(), workspace, "model", &fakeTranslator{}, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("recovery translated %d batches", len(provider.calls))
	}
	writeTranslationCatalog(t, workspace, nil)
	if err := translateWorkspace(context.Background(), workspace, "model", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if entries := readTranslationCache(t, path).Entries; len(entries) != 0 || entries == nil {
		t.Fatalf("empty entries = %#v", entries)
	}
}

func TestTranslationCacheV2ImportsOnlySelectedValidLegacyCache(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	protected, _ := tokenprotect.Protect("One")
	entry := TranslationCacheEntryV1{ID: "a", CacheKey: strings.Repeat("1", 64), SourceSHA256: sha256Hex("One"), TokenSignature: tokenSignature(protected.Tokens()), Translation: "Uno", TranslationSHA256: sha256Hex("Uno")}
	legacy := TranslationCacheV1{Schema: "modpack-translator.translations/v1", Provider: "ollama", Model: "selected", TargetLocale: targetLanguageCode, PromptVersion: translationPromptV1, Entries: []TranslationCacheEntryV1{entry}}
	data, _ := json.Marshal(legacy)
	selectedPath := legacyTranslationCachePath(workspace, "selected")
	writeFiles(t, "", map[string][]byte{selectedPath: data, legacyTranslationCachePath(workspace, "other"): []byte(`{"bad":true}`)})
	provider := &fakeTranslator{}
	if err := translateWorkspace(context.Background(), workspace, "selected", provider, translationOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("valid selected legacy cache was not reused: %#v", provider.calls)
	}
	cache := readTranslationCache(t, translationCachePath(workspace, "selected"))
	if len(cache.Entries) != 1 || cache.Entries[0].Provider != "ollama" || cache.Entries[0].Model != "selected" {
		t.Fatalf("cache=%#v", cache)
	}
	if got := string(mustRead(t, selectedPath)); got != string(data) {
		t.Fatal("legacy v1 cache was modified")
	}
}

func TestTranslationCacheV2PrecedenceAndMalformedLegacyFailClosed(t *testing.T) {
	workspace := t.TempDir()
	writeTranslationCatalog(t, workspace, []CatalogEntryV1{catalogTranslationEntry("a", "One", "a")})
	writeFiles(t, "", map[string][]byte{legacyTranslationCachePath(workspace, "selected"): []byte(`{"bad":true}`)})
	if err := translateWorkspace(context.Background(), workspace, "selected", &fakeTranslator{}, translationOptions{}); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("malformed legacy error=%v", err)
	}
	valid := TranslationCacheV2{Schema: translationSchema, TargetLocale: targetLanguageCode, PromptVersion: translationPromptV1, Entries: []TranslationCacheEntryV2{}}
	data, _ := json.Marshal(valid)
	writeFiles(t, "", map[string][]byte{translationCachePath(workspace, "selected"): data})
	if err := translateWorkspace(context.Background(), workspace, "selected", &fakeTranslator{}, translationOptions{}); err != nil {
		t.Fatalf("v2 did not take precedence: %v", err)
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
	if err := translateWorkspace(context.Background(), workspace, "model", &fakeTranslator{}, translationOptions{}); err != nil {
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
