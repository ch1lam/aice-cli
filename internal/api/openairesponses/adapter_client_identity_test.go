package openairesponses_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/buildinfo"
)

func TestAdapterSendsVersionedClientIdentity(t *testing.T) {
	t.Parallel()
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client(),
		Headers: http.Header{"User-Agent": {"generic-sdk/1.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(t.Context(), apitest.MinimalRequest(openairesponses.API))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	header := <-received
	want := "aice/" + buildinfo.Version
	if values := header.Values("User-Agent"); len(values) != 1 || values[0] != want {
		t.Fatalf("User-Agent = %q, want a single %q", values, want)
	}
}
