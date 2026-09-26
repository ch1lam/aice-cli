package claudesubscription

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	anthropicprovider "github.com/ch1lam/aice-cli/internal/provider/anthropic"
)

func subscriptionRequest() llm.Request {
	request := apitest.MinimalRequest(DefaultModel().API)
	request.Model = DefaultModel()
	request.Options.Thinking = llm.ThinkingLevelHigh
	return request
}

func saveCredential(t *testing.T, paths config.Paths, expiry time.Time) {
	t.Helper()
	_, err := config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		return config.ClaudeSubscriptionCredentials{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: expiry}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStreamsRefreshOnceAndLogoutStopsRequests(t *testing.T) {
	t.Parallel()
	paths := config.Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	saveCredential(t, paths, time.Now().Add(-time.Hour))
	var refreshes, requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			refreshes.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["refresh_token"] != "old-refresh" {
				t.Error("used stale refresh token")
			}
			_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"rotated","expires_in":3600}`)
			return
		}
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer new-access" || r.Header.Get("X-Api-Key") != "" {
			t.Error("incorrect authentication")
		}
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	p := &Provider{paths: paths, auth: AuthClient{TokenURL: server.URL + "/oauth/token"}, baseURL: server.URL}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			stream, err := p.Stream(t.Context(), subscriptionRequest())
			if err != nil {
				t.Error(err)
				return
			}
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 || requests.Load() != 4 {
		t.Fatalf("refreshes/requests = %d/%d", refreshes.Load(), requests.Load())
	}
	stored, err := config.LoadClaudeSubscriptionCredentials(paths)
	if err != nil || stored.RefreshToken != "rotated" {
		t.Fatalf("rotation not persisted: %v", err)
	}
	_, err = config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		return config.ClaudeSubscriptionCredentials{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(t.Context(), subscriptionRequest()); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("logout error = %v", err)
	}
	if requests.Load() != 4 {
		t.Fatal("logout sent a request")
	}
}

func TestRefreshFailurePreservesCredentialWithoutAPIFallback(t *testing.T) {
	t.Parallel()
	paths := config.Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	saveCredential(t, paths, time.Now().Add(-time.Hour))
	before, err := config.LoadClaudeSubscriptionCredentials(paths)
	if err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "old-refresh")
	}))
	defer server.Close()
	p := &Provider{paths: paths, auth: AuthClient{TokenURL: server.URL}, client: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("model request after failed refresh")
		return nil, io.ErrUnexpectedEOF
	})}}
	if _, err := p.Stream(t.Context(), subscriptionRequest()); err == nil || strings.Contains(err.Error(), "old-refresh") {
		t.Fatalf("refresh error = %v", err)
	}
	after, err := config.LoadClaudeSubscriptionCredentials(paths)
	if err != nil || before != after || refreshes.Load() != 1 {
		t.Fatal("refresh failure changed credentials or retried")
	}
}

func TestCatalogAndCredentialBoundaries(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	c := config.Config{AnthropicAPIKey: "api-key", AnthropicBaseURL: "https://other.test"}
	if p.Configured(c) {
		t.Fatal("API key enabled subscription")
	}
	if _, err := p.SaveAPIKey("api-key"); err == nil {
		t.Fatal("accepted API key")
	}
	c.ClaudeSubscriptionCredentials = config.ClaudeSubscriptionCredentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}
	instance, err := p.New(c)
	if err != nil {
		t.Fatal(err)
	}
	if instance.(*Provider).baseURL != BaseURL {
		t.Fatal("subscription inherited API endpoint")
	}
	request := subscriptionRequest()
	request.Model.Provider = "anthropic"
	if _, err := p.Stream(t.Context(), request); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("incompatible request = %v", err)
	}
	for _, model := range Models() {
		if model.Provider != ProviderID || model.Pricing != (llm.Pricing{}) {
			t.Fatal("invalid subscription catalog")
		}
	}
	models := Models()
	models[0].InputModalities[0] = "changed"
	*models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = "changed"
	for _, model := range []llm.Model{DefaultModel(), anthropicprovider.DefaultModel()} {
		if value, _ := model.ThinkingLevelMap.WireValue(llm.ThinkingLevelHigh); value != "high" || model.InputModalities[0] != llm.InputModalityText {
			t.Fatal("catalog state leaked")
		}
	}
}
