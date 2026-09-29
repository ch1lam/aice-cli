package claudesubscription

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
		credential := config.ClaudeSubscriptionCredentials{AccessToken: identity + "-access", RefreshToken: identity + "-refresh", ExpiresAt: time.Now().Add(time.Hour)}
		if _, err := config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
			return credential, nil
		}); err != nil {
			t.Fatal(err)
		}
		stream, err := p.Stream(t.Context(), subscriptionRequest())
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		got := <-headers
		if got.Get("Authorization") != "Bearer "+credential.AccessToken || got.Get("X-Api-Key") != "" {
			t.Fatal("existing provider did not use the replaced OAuth credential")
		}
	}
}
