package llm

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderError preserves the machine-readable metadata needed to decide
// whether a failed model request is safe to retry. Protocol adapters construct
// it; retry policy remains outside the adapters.
type ProviderError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
	Transport  bool
	Err        error
}

// Error returns the original provider or transport error text.
func (e *ProviderError) Error() string {
	if e == nil {
		return "llm: provider error"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("llm: provider returned HTTP %d", e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("llm: provider returned error %q", e.Code)
	}
	return "llm: provider error"
}

// Unwrap preserves cancellation, deadline, and transport error identity.
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewHTTPProviderError normalizes an HTTP API error and any Retry-After hint.
func NewHTTPProviderError(
	err error,
	statusCode int,
	code string,
	header http.Header,
) *ProviderError {
	return &ProviderError{
		StatusCode: statusCode,
		Code:       strings.TrimSpace(code),
		RetryAfter: retryAfterDelay(header, time.Now()),
		Err:        err,
	}
}

// NewTransportProviderError identifies an error that prevented a complete
// provider stream but did not contain an HTTP API response.
func NewTransportProviderError(err error) *ProviderError {
	return &ProviderError{Transport: true, Err: err}
}

func retryAfterDelay(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	if milliseconds := strings.TrimSpace(header.Get("Retry-After-Ms")); milliseconds != "" {
		if value, err := strconv.ParseFloat(milliseconds, 64); err == nil && value > 0 {
			return time.Duration(value * float64(time.Millisecond))
		}
	}

	retryAfter := strings.TrimSpace(header.Get("Retry-After"))
	if retryAfter == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	when, err := http.ParseTime(retryAfter)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}
