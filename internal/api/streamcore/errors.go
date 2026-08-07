package streamcore

import (
	"net/http"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrorInfo carries the HTTP metadata extracted from an SDK API error.
type ErrorInfo struct {
	StatusCode int
	Code       string
	Header     http.Header
}

// NormalizeError classifies an SDK error as an HTTP or transport provider
// error. A nil info means the error carried no API response.
func NormalizeError(err error, info *ErrorInfo) error {
	if info == nil {
		return llm.NewTransportProviderError(err)
	}
	return llm.NewHTTPProviderError(err, info.StatusCode, info.Code, info.Header)
}
