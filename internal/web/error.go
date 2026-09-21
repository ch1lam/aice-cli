// Package web owns the provider-neutral search and fetch contracts, the pure
// source resolver, domain policy and deterministic model-facing rendering.
// Concrete backends live in subpackages; this package never imports them, the
// configuration loader, the Agent Loop or the TUI.
package web

import (
	"context"
	"errors"
	"fmt"
)

// ErrorCode classifies a web failure for the model and the user without
// exposing request headers, credentials or the complete service configuration.
type ErrorCode string

const (
	CodeInvalidArgument       ErrorCode = "invalid_argument"
	CodeInvalidConfig         ErrorCode = "invalid_config"
	CodeMissingCredentials    ErrorCode = "missing_credentials"
	CodeAuthentication        ErrorCode = "authentication"
	CodeRateLimited           ErrorCode = "rate_limited"
	CodeQuotaExceeded         ErrorCode = "quota_exceeded"
	CodeTimeout               ErrorCode = "timeout"
	CodeCanceled              ErrorCode = "canceled"
	CodeUnavailable           ErrorCode = "unavailable"
	CodeInvalidResponse       ErrorCode = "invalid_response"
	CodeResponseTooLarge      ErrorCode = "response_too_large"
	CodeUnsupportedConstraint ErrorCode = "unsupported_constraint"
	CodeConstraintConflict    ErrorCode = "constraint_conflict"
	CodeBlockedTarget         ErrorCode = "blocked_target"
	CodeUnsupportedContent    ErrorCode = "unsupported_content"
	CodeRedirectRefused       ErrorCode = "redirect_refused"
)

// Error is a classified web failure. Message is safe to show to the model and
// the user. Cause retains the original error for errors.Is/As without being
// rendered by Error().
type Error struct {
	Code       ErrorCode
	Message    string
	RetryAfter string
	Cause      error
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Unwrap exposes the retained cause so errors.Is(err, context.Canceled) works.
func (e *Error) Unwrap() error { return e.Cause }

// NewError constructs a classified error with a safe message.
func NewError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WrapContextError maps cancellation and deadline errors to their codes while
// retaining the cause. Other errors map to the fallback code.
func WrapContextError(err error, fallback ErrorCode, message string) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Code: CodeCanceled, Message: "request canceled", Cause: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: CodeTimeout, Message: "request deadline exceeded", Cause: err}
	default:
		return &Error{Code: fallback, Message: message, Cause: err}
	}
}

// CodeOf returns the classification of an error, or the empty code.
func CodeOf(err error) ErrorCode {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Code
	}
	return ""
}
