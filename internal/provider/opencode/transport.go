package opencode

import (
	"crypto/rand"
	"net/http"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// routingClient preserves caller settings without mutating a shared client.
// All three SDK adapters use this provider-owned transport.
func routingClient(client *http.Client) *http.Client {
	wrapped := *http.DefaultClient
	if client != nil {
		wrapped = *client
	}
	base := wrapped.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped.Transport = &routingTransport{base: base, fallbackID: rand.Text()}
	return &wrapped
}

type routingTransport struct {
	base       http.RoundTripper
	fallbackID string
}

func (t *routingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	id := llm.SessionID(request.Context())
	if id == "" {
		// Direct provider consumers without application metadata share an
		// identity for this provider instance, rather than one per request.
		id = t.fallbackID
	}
	cloned := request.Clone(request.Context())
	cloned.Header.Set("x-opencode-session", id)
	cloned.Header.Set("User-Agent", "aice")
	return t.base.RoundTrip(cloned)
}
