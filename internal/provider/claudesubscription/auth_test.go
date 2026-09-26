package claudesubscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestBrowserOAuthPKCEAndManualInput(t *testing.T) {
	t.Parallel()
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "callback", true: "manual"}[manual], func(t *testing.T) {
			var authorization *url.URL
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				challenge := sha256.Sum256([]byte(body["code_verifier"]))
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || body["client_id"] != clientID || body["grant_type"] != "authorization_code" || body["code"] != "valid-code" || body["redirect_uri"] != redirectURI || body["state"] != authorization.Query().Get("state") || base64.RawURLEncoding.EncodeToString(challenge[:]) != authorization.Query().Get("code_challenge") {
					t.Error("incorrect OAuth exchange")
				}
				_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
			}))
			defer server.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := "http://" + listener.Addr().String() + "/callback"
			input := make(chan string, 1)
			client := AuthClient{TokenURL: server.URL}
			credential, err := client.authorizeBrowser(t.Context(), LoginInteraction{
				Input: input, Notify: func(_ context.Context, prompt LoginPrompt) error {
					if strings.Contains(prompt.Instructions, "mismatch") {
						return errors.New("unexpected mismatch")
					}
					authorization, err = url.Parse(prompt.URL)
					if err != nil {
						return err
					}
					if authorization.Host != "claude.ai" || authorization.Query().Get("scope") != scopes || authorization.Query().Get("code_challenge_method") != "S256" {
						t.Error("incorrect authorization URL")
					}
					state := authorization.Query().Get("state")
					if manual {
						input <- "valid-code#" + state
						return nil
					}
					for _, supplied := range []string{"wrong", state} {
						response, err := http.Get(address + "?code=valid-code&state=" + supplied)
						if err != nil {
							return err
						}
						response.Body.Close()
						if supplied == "wrong" && response.StatusCode != 400 {
							t.Error("accepted wrong state")
						}
					}
					return nil
				},
			}, listener)
			if err != nil || !credential.Configured() || time.Until(credential.ExpiresAt) < 59*time.Minute {
				t.Fatalf("login failed: %v", err)
			}
			if connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
				connection.Close()
				t.Fatal("callback listener leaked")
			}
		})
	}
}

func TestOAuthRefreshErrorsAndRedirects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		status     int
		good       bool
	}{
		{"rotate", `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`, 200, true},
		{"retain refresh", `{"access_token":"new","expires_in":3600}`, 200, true},
		{"invalid expiry", `{"access_token":"new","expires_in":-1}`, 200, false},
		{"invalid token", `{"access_token":"bad\nheader","expires_in":3600}`, 200, false},
		{"server error", `private-refresh`, 401, false},
		{"oversize", strings.Repeat("x", (1<<20)+1), 200, false},
		{"redirect", `private-refresh`, 307, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["refresh_token"] != "private-refresh" || body["grant_type"] != "refresh_token" || body["client_id"] != clientID {
					t.Error("incorrect refresh request")
				}
				if r.URL.Path == "/must-not-follow" {
					t.Error("followed token redirect")
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got, err := (AuthClient{TokenURL: server.URL}).Refresh(t.Context(), config.ClaudeSubscriptionCredentials{RefreshToken: "private-refresh"})
			if tc.good {
				if err != nil || !got.Configured() {
					t.Fatalf("refresh: %v", err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "private-refresh") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}

func TestOAuthCancellationAndPortCollision(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := AuthClient{}
	_, err = client.listenAndAuthorizeBrowser(t.Context(), LoginInteraction{Notify: func(context.Context, LoginPrompt) error { t.Error("opened colliding flow"); return nil }}, listener.Addr().String())
	if err == nil {
		t.Fatal("accepted occupied port")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = client.authorizeBrowser(ctx, LoginInteraction{Notify: func(context.Context, LoginPrompt) error { cancel(); return nil }}, listener)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
