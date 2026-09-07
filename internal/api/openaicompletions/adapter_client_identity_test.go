package openaicompletions_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
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
	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(t.Context(), apitest.MinimalRequest(openaicompletions.API))
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
