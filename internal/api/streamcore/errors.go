package streamcore

import (
	"cmp"
	"encoding/json"
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

// StreamEventError classifies an error event received inside an accepted
// stream from its {"error":{...}} payload. The code, or else the type, decides
// whether a retry can help; a numeric HTTP status code, as some gateways send,
// is kept as the status. A payload without either is not transient.
func StreamEventError(err error, data []byte) error {
	var payload struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &payload) // A malformed payload carries no code.
	providerErr := &llm.ProviderError{Code: payload.Error.Type, Err: err}
	switch code := payload.Error.Code.(type) {
	case string:
		providerErr.Code = cmp.Or(code, payload.Error.Type)
	case float64:
		if code >= 400 && code < 600 {
			providerErr.StatusCode = int(code)
		}
	}
	return providerErr
}
