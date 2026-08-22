package provider

import (
	"context"
	"fmt"
	"time"
)

type Request struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceKind   string `json:"source_kind"`
	SourceFile   string `json:"source_file"`
	TargetLocale string `json:"target_locale"`
}

type Result struct {
	ID         string `json:"id"`
	Translated string `json:"translated"`
}

type Identity struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Limits describes a provider model's configured translation capacity.
type Limits struct {
	ContextTokens   int
	MaxOutputTokens int
	MaxRequestBytes int
	MaxEntries      int
}

type Batch struct {
	Results  []Result
	Identity Identity
}

type Translator interface {
	Translate(context.Context, []Request) (Batch, error)
}

// Legacy internal names keep the implementation and package-local tests concise.
type TranslationRequest = Request
type TranslationResult = Result
type ProviderIdentity = Identity
type TranslationBatch = Batch
type TranslationErrorKind = ErrorKind
type ProviderError = Error

type ErrorKind string

const (
	ErrorQuota       ErrorKind = "quota_exhausted"
	ErrorAuth        ErrorKind = "authentication"
	ErrorPermission  ErrorKind = "permission"
	ErrorConfig      ErrorKind = "configuration"
	ErrorRequest     ErrorKind = "invalid_request"
	ErrorModel       ErrorKind = "model_incompatible"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorCancelled   ErrorKind = "cancelled"
	ErrorUnknown     ErrorKind = "unknown"
)

type Error struct {
	Identity Identity
	Kind     ErrorKind
	Reason   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s provider (%s) failed: %s", e.Identity.Provider, e.Kind, e.Reason)
}

type invalidResponseError struct {
	err error
}

func (e *invalidResponseError) Error() string    { return e.err.Error() }
func (e *invalidResponseError) Unwrap() error    { return e.err }
func (e *invalidResponseError) InvalidResponse() {}

type invalidTranslationResponseError = invalidResponseError

type sleeper func(context.Context, time.Duration) error

func translationSchemaFor(count int, arrayLength bool) map[string]any {
	results := map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "translated"}, "properties": map[string]any{"id": map[string]any{"type": "string"}, "translated": map[string]any{"type": "string"}}}}
	if arrayLength {
		results["minItems"], results["maxItems"] = count, count
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"results"}, "properties": map[string]any{"results": results}}
}
