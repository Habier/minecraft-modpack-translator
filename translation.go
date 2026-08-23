package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"modpack-translator/tokenprotect"
)

const (
	translationSchema   = "modpack-translator.translations/v2"
	translationPromptV2 = "minecraft-localization-system-user-v2"
	validationRetries   = 1
	maxCatalogFileBytes = 320 << 20
)

type TranslationCacheV2 struct {
	Schema        string                    `json:"schema"`
	TargetLocale  string                    `json:"target_locale"`
	PromptVersion string                    `json:"prompt_version"`
	Entries       []TranslationCacheEntryV2 `json:"entries"`
}

type TranslationCacheEntryV2 struct {
	ID                string `json:"id"`
	CacheKey          string `json:"cache_key"`
	SourceSHA256      string `json:"source_sha256"`
	TokenSignature    string `json:"token_signature"`
	Translation       string `json:"translation"`
	TranslationSHA256 string `json:"translation_sha256"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
}

type translationOptions struct{}

type TranslationPartialError struct {
	Successful int
	Cached     int
	Failed     int
	ReportPath string
}

func (e *TranslationPartialError) Error() string {
	return fmt.Sprintf("translation completed partially: successful=%d cached=%d failed=%d; retry pending entries by rerunning --translate; report: %s", e.Successful, e.Cached, e.Failed, e.ReportPath)
}

type translationFailureReport struct {
	Schema   string               `json:"schema"`
	Failures []translationFailure `json:"failures"`
}

type translationFailure struct {
	ID       string `json:"id"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason"`
}

var (
	translationLogMu sync.Mutex
)

func appendTranslationLog(format string, args ...any) {
	path := "translation.log"
	translationLogMu.Lock()
	defer translationLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), msg)
}

type preparedTranslation struct {
	entry          CatalogEntryV1
	protected      *tokenprotect.Text
	key            string
	sourceHash     string
	tokenSignature string
}

type validatedTranslation struct {
	text     string
	identity ProviderIdentity
}

func translateWorkspace(ctx context.Context, workspace string, translator Translator, options translationOptions) error {
	_ = options
	catalog, err := loadCatalog(filepath.Join(workspace, "catalog", "catalog.v1.json"))
	if err != nil {
		return err
	}
	cachePath := translationCachePath(workspace, catalog.TargetLocale)
	cache, err := loadTranslationCacheV2(cachePath, catalog.TargetLocale)
	if err != nil {
		return err
	}
	promptMatches := cache.PromptVersion == translationPromptV2
	cache.PromptVersion = translationPromptV2
	if !promptMatches {
		cache.Entries = []TranslationCacheEntryV2{}
	}

	prepared := make([]preparedTranslation, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		protected, err := tokenprotect.Protect(entry.Source)
		if err != nil {
			return fmt.Errorf("protect catalog entry %s: %w", entry.ID, err)
		}
		sourceHash := sha256Hex(entry.Source)
		signature := tokenSignature(protected.Tokens())
		key := strings.Join([]string{sha256Hex(protected.Protected), signature, catalog.TargetLocale, translationPromptV2}, "|")
		prepared = append(prepared, preparedTranslation{entry: entry, protected: protected, key: key, sourceHash: sourceHash, tokenSignature: signature})
	}

	validByID := make(map[string]TranslationCacheEntryV2)
	for _, cached := range cache.Entries {
		validByID[cached.ID] = cached
	}
	cachedCount := 0
	groups := make(map[string][]preparedTranslation)
	var representatives []preparedTranslation
	for _, item := range prepared {
		if cached, ok := validByID[item.entry.ID]; ok && promptMatches && cached.SourceSHA256 == item.sourceHash && cached.TokenSignature == item.tokenSignature && cached.TranslationSHA256 == sha256Hex(cached.Translation) {
			cached.CacheKey = sha256Hex(item.key)
			validByID[item.entry.ID] = cached
			cachedCount++
			continue
		}
		if _, exists := groups[item.key]; !exists {
			representatives = append(representatives, item)
		}
		groups[item.key] = append(groups[item.key], item)
	}
	sort.SliceStable(representatives, func(i, j int) bool {
		leftMod, rightMod := catalogModIdentity(representatives[i].entry), catalogModIdentity(representatives[j].entry)
		if leftMod != rightMod {
			return leftMod < rightMod
		}
		leftFile := filepath.ToSlash(filepath.Clean(representatives[i].entry.SourceFile))
		rightFile := filepath.ToSlash(filepath.Clean(representatives[j].entry.SourceFile))
		return leftFile < rightFile
	})
	fmt.Printf("Translation progress: total=%d cached=%d translated=0 remaining=%d\n", len(prepared), cachedCount, len(prepared)-cachedCount)
	appendTranslationLog("start total=%d cached=%d remaining=%d", len(prepared), cachedCount, len(prepared)-cachedCount)
	if len(prepared) == 0 {
		cache.Entries = []TranslationCacheEntryV2{}
		if err := publishTranslationCache(cachePath, cache); err != nil {
			return err
		}
		if err := os.Remove(translationFailureReportPath(workspace, catalog.TargetLocale)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale translation failure report: %w", err)
		}
		appendTranslationLog("done empty catalog")
		fmt.Println("Translation summary: successful=0 cached=0 failed=0")
		return nil
	}
	translatedCount := 0
	failures := []translationFailure{}
	publishValidated := func(validated map[string]validatedTranslation, byID map[string]preparedTranslation) error {
		for id, result := range validated {
			item := byID[id]
			for _, occurrence := range groups[item.key] {
				validByID[occurrence.entry.ID] = TranslationCacheEntryV2{ID: occurrence.entry.ID, CacheKey: sha256Hex(occurrence.key), SourceSHA256: occurrence.sourceHash, TokenSignature: occurrence.tokenSignature, Translation: result.text, TranslationSHA256: sha256Hex(result.text), Provider: result.identity.Provider, Model: result.identity.Model}
				translatedCount++
			}
		}
		cache.Entries = cacheEntriesInCatalogOrder(prepared, validByID)
		return publishTranslationCache(cachePath, cache)
	}
	var processBatch func([]TranslationRequest, map[string]preparedTranslation) error
	processBatch = func(requests []TranslationRequest, byID map[string]preparedTranslation) error {
		if len(requests) == 0 {
			return nil
		}
		var validationErr error
		var lastIdentity ProviderIdentity
		pending := append([]TranslationRequest(nil), requests...)
		pendingByID := make(map[string]preparedTranslation, len(byID))
		for id, item := range byID {
			pendingByID[id] = item
		}
		for attempt := 0; attempt <= validationRetries; attempt++ {
			batch, err := translator.Translate(ctx, pending)
			if batch.Identity.Provider != "" {
				lastIdentity = batch.Identity
			}
			validated, partialValidationErr := validatePartialTranslationResults(batch.Results, pendingByID)
			if len(validated) > 0 {
				if publishErr := publishValidated(validated, pendingByID); publishErr != nil {
					return publishErr
				}
				pending, pendingByID = removeValidatedRequests(pending, pendingByID, validated)
			}
			if len(pending) == 0 {
				switch {
				case err != nil:
					appendTranslationLog("response anomaly after completed results provider=%s model=%s entries=%d err=%s", lastIdentity.Provider, lastIdentity.Model, len(requests), sanitizeFailureReason(err.Error()))
				case partialValidationErr != nil:
					appendTranslationLog("response anomaly after completed results provider=%s model=%s entries=%d err=%s", lastIdentity.Provider, lastIdentity.Model, len(requests), sanitizeFailureReason(partialValidationErr.Error()))
				default:
					appendTranslationLog("validated provider=%s model=%s entries=%d", batch.Identity.Provider, batch.Identity.Model, len(requests))
					fmt.Printf("Validated batch: provider=%s model=%s entries=%d\n", batch.Identity.Provider, batch.Identity.Model, len(requests))
				}
				return nil
			}
			if err != nil {
				var invalid interface {
					error
					InvalidResponse()
				}
				if !errors.As(err, &invalid) {
					appendTranslationLog("fatal provider=%s model=%s entries=%d err=%s", lastIdentity.Provider, lastIdentity.Model, len(pending), err)
					return err
				}
				appendTranslationLog("invalid response provider=%s model=%s entries=%d attempt=%d err=%s", lastIdentity.Provider, lastIdentity.Model, len(pending), attempt+1, err)
				validationErr = invalid
			} else if partialValidationErr != nil {
				appendTranslationLog("validation failed provider=%s model=%s entries=%d attempt=%d err=%s", batch.Identity.Provider, batch.Identity.Model, len(pending), attempt+1, partialValidationErr)
				validationErr = partialValidationErr
			} else {
				validationErr = fmt.Errorf("missing result ID %q", pending[0].ID)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if len(pending) == 1 {
			item := pendingByID[pending[0].ID]
			appendTranslationLog("singleton failure provider=%s model=%s id=%s reason=%s", lastIdentity.Provider, lastIdentity.Model, pending[0].ID, sanitizeFailureReason(validationErr.Error()))
			for _, occurrence := range groups[item.key] {
				failures = append(failures, translationFailure{ID: occurrence.entry.ID, Provider: lastIdentity.Provider, Model: lastIdentity.Model, Kind: "validation", Reason: sanitizeFailureReason(validationErr.Error())})
			}
			return nil
		}
		middle := len(pending) / 2
		appendTranslationLog("splitting provider=%s model=%s entries=%d into %d+%d", lastIdentity.Provider, lastIdentity.Model, len(pending), len(pending[:middle]), len(pending[middle:]))
		for _, half := range [][]TranslationRequest{pending[:middle], pending[middle:]} {
			halfByID := make(map[string]preparedTranslation, len(half))
			for _, request := range half {
				halfByID[request.ID] = pendingByID[request.ID]
			}
			if err := processBatch(half, halfByID); err != nil {
				return err
			}
		}
		return nil
	}
	orderedRequests := make([]TranslationRequest, 0, len(representatives))
	orderedByID := make(map[string]preparedTranslation, len(representatives))
	for _, item := range representatives {
		request := TranslationRequest{ID: item.entry.ID, Source: item.protected.Protected, SourceKind: item.entry.SourceKind, SourceFile: filepath.ToSlash(item.entry.SourceFile), TargetLocale: catalog.TargetLocale}
		orderedRequests = append(orderedRequests, request)
		orderedByID[request.ID] = item
	}
	plans, err := translator.Plan(orderedRequests)
	if err != nil {
		return fmt.Errorf("plan translation batches: %w", err)
	}
	for batchNumber, requests := range plans {
		byID := make(map[string]preparedTranslation, len(requests))
		for _, request := range requests {
			byID[request.ID] = orderedByID[request.ID]
		}
		fmt.Printf("Translation batch %d: entries=%d remaining=%d\n", batchNumber+1, len(requests), len(prepared)-cachedCount-translatedCount)
		appendTranslationLog("batch %d entries=%d remaining=%d", batchNumber+1, len(requests), len(prepared)-cachedCount-translatedCount)
		if err := processBatch(requests, byID); err != nil {
			return fmt.Errorf("translate batch %d: %w", batchNumber+1, err)
		}
		appendTranslationLog("progress total=%d cached=%d translated=%d remaining=%d", len(prepared), cachedCount, translatedCount, len(prepared)-cachedCount-translatedCount)
		fmt.Printf("Translation progress: total=%d cached=%d translated=%d remaining=%d\n", len(prepared), cachedCount, translatedCount, len(prepared)-cachedCount-translatedCount)
	}
	reportPath := translationFailureReportPath(workspace, catalog.TargetLocale)
	cache.Entries = cacheEntriesInCatalogOrder(prepared, validByID)
	if err := publishTranslationCache(cachePath, cache); err != nil {
		return err
	}
	if len(failures) > 0 {
		sort.Slice(failures, func(i, j int) bool { return failures[i].ID < failures[j].ID })
		if err := publishTranslationFailureReport(reportPath, translationFailureReport{Schema: "modpack-translator.translation-failures/v2", Failures: failures}); err != nil {
			return err
		}
		fmt.Printf("Translation summary: successful=%d cached=%d failed=%d (partial)\n", translatedCount, cachedCount, len(failures))
		appendTranslationLog("done partial successful=%d cached=%d failed=%d", translatedCount, cachedCount, len(failures))
		for i, failure := range failures {
			if i == 10 {
				fmt.Printf("  ... and %d more; see %s\n", len(failures)-i, reportPath)
				break
			}
			fmt.Printf("  %s: %s\n", failure.ID, failure.Reason)
		}
		return &TranslationPartialError{Successful: translatedCount, Cached: cachedCount, Failed: len(failures), ReportPath: reportPath}
	}
	if err := os.Remove(reportPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale translation failure report: %w", err)
	}
	appendTranslationLog("done successful=%d cached=%d failed=0", translatedCount, cachedCount)
	fmt.Printf("Translation summary: successful=%d cached=%d failed=0\n", translatedCount, cachedCount)
	return nil
}

func catalogModIdentity(entry CatalogEntryV1) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(entry.SourceFile)), "/")
	switch {
	case len(parts) >= 2 && parts[0] == "assets":
		return "namespace:" + parts[1]
	case len(parts) >= 4 && parts[0] == "sources" && parts[1] == "kubejs" && parts[2] == "assets":
		return "namespace:" + parts[3]
	case len(parts) >= 4 && parts[0] == "sources" && parts[1] == "patchouli" && (parts[2] == "jars" || parts[2] == "instance"):
		return "patchouli:" + parts[3]
	case len(parts) >= 2 && parts[0] == "sources" && parts[1] == "ftbquests":
		return "ftbquests"
	default:
		return entry.SourceKind
	}
}

func validateTranslationResults(results []TranslationResult, requested map[string]preparedTranslation) (map[string]validatedTranslation, error) {
	if len(results) != len(requested) {
		return nil, fmt.Errorf("received %d results for %d requested IDs", len(results), len(requested))
	}
	validated, err := validatePartialTranslationResults(results, requested)
	if err != nil {
		return nil, err
	}
	for id := range requested {
		if _, ok := validated[id]; !ok {
			return nil, fmt.Errorf("missing result ID %q", id)
		}
	}
	return validated, nil
}

func validatePartialTranslationResults(results []TranslationResult, requested map[string]preparedTranslation) (map[string]validatedTranslation, error) {
	validated := make(map[string]validatedTranslation, len(results))
	invalid := make(map[string]bool)
	var firstErr error
	for _, result := range results {
		item, ok := requested[result.ID]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("unknown result ID %q", result.ID)
			}
			continue
		}
		if _, duplicate := validated[result.ID]; duplicate {
			delete(validated, result.ID)
			invalid[result.ID] = true
			if firstErr == nil {
				firstErr = fmt.Errorf("duplicate result ID %q", result.ID)
			}
			continue
		}
		if invalid[result.ID] {
			if firstErr == nil {
				firstErr = fmt.Errorf("duplicate result ID %q", result.ID)
			}
			continue
		}
		if !utf8.ValidString(result.Translated) {
			invalid[result.ID] = true
			if firstErr == nil {
				firstErr = fmt.Errorf("result %s is not UTF-8", result.ID)
			}
			continue
		}
		restored, err := item.protected.Restore(result.Translated)
		if err != nil {
			invalid[result.ID] = true
			if firstErr == nil {
				firstErr = fmt.Errorf("result %s: %w", result.ID, err)
			}
			continue
		}
		validated[result.ID] = validatedTranslation{text: restored, identity: result.Identity}
	}
	return validated, firstErr
}

func removeValidatedRequests(requests []TranslationRequest, byID map[string]preparedTranslation, validated map[string]validatedTranslation) ([]TranslationRequest, map[string]preparedTranslation) {
	unfinished := make([]TranslationRequest, 0, len(requests)-len(validated))
	unfinishedByID := make(map[string]preparedTranslation, len(byID)-len(validated))
	for _, request := range requests {
		if _, done := validated[request.ID]; done {
			continue
		}
		unfinished = append(unfinished, request)
		unfinishedByID[request.ID] = byID[request.ID]
	}
	return unfinished, unfinishedByID
}

func loadCatalog(path string) (CatalogV1, error) {
	data, err := readFileLimited(path, maxCatalogFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return CatalogV1{}, fmt.Errorf("translation catalog is absent at %s; extraction must complete first", path)
	}
	if err != nil {
		return CatalogV1{}, fmt.Errorf("read translation catalog: %w", err)
	}
	var catalog CatalogV1
	if err := decodeStrictJSON(data, &catalog); err != nil {
		return CatalogV1{}, fmt.Errorf("parse translation catalog: %w", err)
	}
	if catalog.Schema != catalogSchema || catalog.SourceLocale != sourceLanguageCode {
		return CatalogV1{}, errors.New("translation catalog has unsupported schema or locales")
	}
	if normalized, ok := normalizeMinecraftLocale(catalog.TargetLocale); !ok || normalized != catalog.TargetLocale {
		return CatalogV1{}, errors.New("translation catalog has unsupported schema or locales")
	}
	seen := make(map[string]bool, len(catalog.Entries))
	for i, entry := range catalog.Entries {
		if entry.ID == "" || seen[entry.ID] || entry.TargetLocale != catalog.TargetLocale || entry.SourceKind == "" || entry.SourceFile == "" || filepath.IsAbs(entry.SourceFile) || entry.Locator == "" || !utf8.ValidString(entry.Source) {
			return CatalogV1{}, fmt.Errorf("translation catalog entry %d is invalid", i)
		}
		seen[entry.ID] = true
		found := tokenprotect.Find(entry.Source)
		if len(found) != len(entry.Tokens) {
			return CatalogV1{}, fmt.Errorf("translation catalog entry %s token metadata mismatch", entry.ID)
		}
		for j := range found {
			if string(found[j].Kind) != entry.Tokens[j].Kind || found[j].Text != entry.Tokens[j].Text || found[j].Start != entry.Tokens[j].Start || found[j].End != entry.Tokens[j].End {
				return CatalogV1{}, fmt.Errorf("translation catalog entry %s token metadata mismatch", entry.ID)
			}
		}
	}
	return catalog, nil
}

func loadTranslationCacheV2(path, locale string) (TranslationCacheV2, error) {
	cache := TranslationCacheV2{Schema: translationSchema, TargetLocale: locale, PromptVersion: translationPromptV2, Entries: []TranslationCacheEntryV2{}}
	data, err := readFileLimited(path, 320<<20)
	recovered := false
	if errors.Is(err, os.ErrNotExist) {
		data, err = readFileLimited(path+".previous", 320<<20)
		if errors.Is(err, os.ErrNotExist) {
			return cache, nil
		}
		recovered = true
	}
	if err != nil {
		return TranslationCacheV2{}, fmt.Errorf("read translation cache: %w", err)
	}
	if err := decodeStrictJSON(data, &cache); err != nil {
		return TranslationCacheV2{}, fmt.Errorf("parse translation cache safely: %w", err)
	}
	if cache.Schema != translationSchema || cache.TargetLocale != locale || cache.PromptVersion == "" {
		return TranslationCacheV2{}, errors.New("translation cache metadata does not match the catalog")
	}
	if err := validateCacheV2Entries(cache.Entries); err != nil {
		return TranslationCacheV2{}, err
	}
	if recovered {
		if err := os.Rename(path+".previous", path); err != nil {
			return TranslationCacheV2{}, fmt.Errorf("recover translation cache: %w", err)
		}
	}
	return cache, nil
}

func validateCacheV2Entries(entries []TranslationCacheEntryV2) error {
	seen := map[string]bool{}
	for i, entry := range entries {
		if entry.ID == "" || seen[entry.ID] || entry.Provider == "" || entry.Model == "" || strings.ContainsAny(entry.Provider+entry.Model, "\r\n\x00") || !isSHA256(entry.CacheKey) || !isSHA256(entry.SourceSHA256) || !isSHA256(entry.TokenSignature) || !isSHA256(entry.TranslationSHA256) || !utf8.ValidString(entry.Translation) || sha256Hex(entry.Translation) != entry.TranslationSHA256 {
			return fmt.Errorf("translation cache entry %d is invalid", i)
		}
		seen[entry.ID] = true
	}
	return nil
}

func publishTranslationCache(path string, cache TranslationCacheV2) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create translation cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".translations.v2-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("stage translation cache: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("publish translation cache: %w", err)
	}
	return nil
}

func translationCachePath(workspace string, targetLocale ...string) string {
	locale := selectedTargetLocale(targetLocale...)
	name := "translations.v2.json"
	if locale != defaultTargetLanguageCode {
		name = "translations." + locale + ".v2.json"
	}
	return filepath.Join(workspace, "translations", name)
}

func translationFailureReportPath(workspace string, targetLocale ...string) string {
	locale := selectedTargetLocale(targetLocale...)
	name := "failures.v2.json"
	if locale != defaultTargetLanguageCode {
		name = "failures." + locale + ".v2.json"
	}
	return filepath.Join(workspace, "translations", name)
}

func publishTranslationFailureReport(path string, report translationFailureReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create translation report directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".failures.v2-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("stage translation failure report: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("publish translation failure report: %w", err)
	}
	return nil
}

var unsafeModelName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeModelName(model string) string {
	name := strings.Trim(unsafeModelName.ReplaceAllString(model, "_"), "._-")
	if name == "" {
		name = "model"
	}
	digest := sha256.Sum256([]byte(model))
	return fmt.Sprintf("%s-%x", name, digest[:4])
}
func tokenSignature(tokens []tokenprotect.Token) string {
	data, _ := json.Marshal(tokens)
	return sha256Hex(string(data))
}
func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func isSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
func cacheEntriesInCatalogOrder(items []preparedTranslation, byID map[string]TranslationCacheEntryV2) []TranslationCacheEntryV2 {
	result := make([]TranslationCacheEntryV2, 0, len(items))
	for _, item := range items {
		if entry, ok := byID[item.entry.ID]; ok {
			result = append(result, entry)
		}
	}
	return result
}

func sanitizeFailureReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, reason)
	return truncate(reason, 300)
}
func readFileLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimitedBody(file, limit)
}
func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
