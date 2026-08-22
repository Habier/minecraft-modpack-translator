package main

import (
	"modpack-translator/internal/provider"
)

type TranslationRequest = provider.Request
type TranslationResult = provider.Result
type ProviderIdentity = provider.Identity
type TranslationBatch = provider.Batch
type Translator = provider.Translator
type TranslationErrorKind = provider.ErrorKind
type ProviderError = provider.Error

type invalidTranslationResponseError struct {
	err error
}

func (e *invalidTranslationResponseError) Error() string    { return e.err.Error() }
func (e *invalidTranslationResponseError) Unwrap() error    { return e.err }
func (e *invalidTranslationResponseError) InvalidResponse() {}

const (
	ErrorQuota       = provider.ErrorQuota
	ErrorAuth        = provider.ErrorAuth
	ErrorPermission  = provider.ErrorPermission
	ErrorConfig      = provider.ErrorConfig
	ErrorRequest     = provider.ErrorRequest
	ErrorModel       = provider.ErrorModel
	ErrorUnavailable = provider.ErrorUnavailable
	ErrorCancelled   = provider.ErrorCancelled
	ErrorUnknown     = provider.ErrorUnknown
)
