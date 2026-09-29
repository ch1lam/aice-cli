package codex

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestExistingProviderReadsReplacedLogin(t *testing.T) {
	t.Parallel()
	paths := config.Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	headers := make(chan http.Header, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	p := &Provider{paths: paths, baseURL: server.URL}
	for _, identity := range []string{"old", "new"} {
		credential := config.CodexCredentials{AccessToken: identity + "-access", RefreshToken: identity + "-refresh", AccountID: identity + "-account", ExpiresAt: time.Now().Add(time.Hour)}
		if _, err := config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return credential, nil }); err != nil {
			t.Fatal(err)
		}
		stream, err := p.Stream(t.Context(), codexRequest())
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		got := <-headers
		if got.Get("Authorization") != "Bearer "+credential.AccessToken || got.Get("Chatgpt-Account-Id") != credential.AccountID {
			t.Fatal("existing provider did not use the replaced login identity")
		}
	}
}
