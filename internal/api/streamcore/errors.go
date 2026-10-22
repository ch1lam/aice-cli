package streamcore

import (
	"errors"
	"net/http"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// SDKError classifies an SDK failure. When err wraps the SDK's API error type
// E, describe reports its HTTP status, error code and optional response; any
// other failure carried no API response and is a transport error.
func SDKError[E error](
	err error,
	describe func(E) (status int, code string, response *http.Response),
) error {
	var apiErr E
	if !errors.As(err, &apiErr) {
		return llm.NewTransportProviderError(err)
	}
	status, code, response := describe(apiErr)
	var header http.Header
	if response != nil {
		header = response.Header
	}
	return llm.NewHTTPProviderError(err, status, code, header)
}
