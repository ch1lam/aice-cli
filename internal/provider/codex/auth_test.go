package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

func testToken() string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"test-account"}}`)) + ".signature"
}

type outputFunc func([]byte) (int, error)

func (f outputFunc) Write(data []byte) (int, error) { return f(data) }

func TestBrowserLoginChecksStateAndExchangesPKCE(t *testing.T) {
	t.Parallel()
	var challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/oauth/token" || r.Form.Get("client_id") != clientID || r.Form.Get("code") != "authorized" || r.Form.Get("redirect_uri") != redirectURI {
			t.Error("incorrect authorization exchange")
		}
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
			t.Error("PKCE mismatch")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": testToken(), "refresh_token": "refresh", "expires_in": 3600})
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	output := outputFunc(func(data []byte) (int, error) {
		var authURL *url.URL
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, server.URL) {
				authURL, err = url.Parse(line)
				if err != nil {
					return 0, err
				}
			}
		}
		if authURL == nil {
			return 0, errors.New("missing authorization URL")
		}
		challenge = authURL.Query().Get("code_challenge")
		if authURL.Query().Get("code_challenge_method") != "S256" {
			t.Error("missing PKCE method")
		}
		for _, state := range []string{"wrong", authURL.Query().Get("state")} {
			response, err := http.Get("http://" + listener.Addr().String() + "/auth/callback?code=authorized&state=" + url.QueryEscape(state))
			if err != nil {
				return 0, err
			}
			_ = response.Body.Close()
			if state == "wrong" && response.StatusCode != http.StatusBadRequest {
				t.Error("accepted wrong state")
			}
		}
		return len(data), nil
	})
	credential, err := (AuthClient{BaseURL: server.URL}).browserLogin(t.Context(), output, listener)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "test-account" || credential.RefreshToken != "refresh" {
		t.Error("incorrect credentials")
	}
}

func TestBrowserLoginCancellationClosesListener(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	_, err = (AuthClient{}).browserLogin(ctx, outputFunc(func(data []byte) (int, error) { cancel(); return len(data), nil }), listener)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	reopened, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("callback listener leaked: %v", err)
	}
	_ = reopened.Close()
}

func TestRefreshValidatesResponseAndDoesNotExposeSecrets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"rotated", fmt.Sprintf(`{"access_token":%q,"refresh_token":"rotated","expires_in":3600}`, testToken()), 200, true},
		{"unchanged refresh", fmt.Sprintf(`{"access_token":%q,"expires_in":3600}`, testToken()), 200, true},
		{"denied", `{"error":"secret-refresh"}`, 401, false},
		{"malformed", `secret-refresh`, 200, false},
		{"missing account", `{"access_token":"a.e30.b","refresh_token":"secret-refresh","expires_in":3600}`, 200, false},
		{"negative expiry", `{"access_token":"secret-refresh","expires_in":-1}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("refresh_token") != "secret-refresh" || r.Form.Get("grant_type") != "refresh_token" {
					t.Error("incorrect refresh request")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			credential, err := (AuthClient{BaseURL: server.URL}).Refresh(t.Context(), config.CodexCredentials{RefreshToken: "secret-refresh"})
			if tc.valid {
				if err != nil || !credential.Configured() || time.Until(credential.ExpiresAt) < 59*time.Minute {
					t.Fatalf("invalid refresh: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-refresh") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}

func TestDeviceLoginPollsPendingAndExchangesCode(t *testing.T) {
	t.Parallel()
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"device","user_code":"ABCD","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(403)
				return
			}
			_, _ = io.WriteString(w, `{"authorization_code":"code","code_verifier":"verifier"}`)
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("code") != "code" || r.Form.Get("code_verifier") != "verifier" || !strings.HasSuffix(r.Form.Get("redirect_uri"), "/deviceauth/callback") {
				t.Error("invalid device exchange")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": testToken(), "refresh_token": "refresh", "expires_in": 3600})
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	var output strings.Builder
	credential, err := (AuthClient{BaseURL: server.URL}).Login(t.Context(), true, &output)
	if err != nil || !credential.Configured() {
		t.Fatalf("device login: %v", err)
	}
	if !strings.Contains(output.String(), "ABCD") || strings.Contains(output.String(), "refresh") {
		t.Fatal("incorrect device instructions")
	}
}

func TestManualBrowserLoginRetriesStateAndExchangesPKCE(t *testing.T) {
	t.Parallel()
	input := make(chan string, 1)
	var challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "manual-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
			t.Error("manual exchange lost PKCE")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": testToken(), "refresh_token": "refresh", "expires_in": 3600})
	}))
	defer server.Close()
	prompts := 0
	credential, err := (AuthClient{BaseURL: server.URL}).authorizeBrowser(t.Context(), LoginInteraction{Input: input, Notify: func(ctx context.Context, prompt LoginPrompt) error {
		prompts++
		address, _ := url.Parse(prompt.URL)
		challenge = address.Query().Get("code_challenge")
		if !prompt.AllowInput {
			t.Error("manual fallback disabled")
		}
		if prompts == 1 {
			input <- "http://localhost:1455/auth/callback?code=private-rejected-code&state=wrong"
		} else {
			if strings.Contains(prompt.Instructions, "private-rejected-code") {
				t.Error("retry leaked code")
			}
			input <- "http://localhost:1455/auth/callback?code=manual-code&state=" + address.Query().Get("state")
		}
		return nil
	}}, nil)
	if err != nil || !credential.Configured() || prompts != 2 {
		t.Fatalf("manual login: prompts=%d err=%v", prompts, err)
	}
}

func TestManualBrowserLoginCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	_, err := (AuthClient{}).authorizeBrowser(ctx, LoginInteraction{Input: make(chan string), Notify: func(context.Context, LoginPrompt) error { cancel(); return nil }}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestBrowserLoginOccupiedPortDoesNotStartAuthorization(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint("manual-input=", manual), func(t *testing.T) {
			interaction := LoginInteraction{Notify: func(context.Context, LoginPrompt) error {
				t.Error("opened authorization while callback belonged to another process")
				return errors.New("unexpected notification")
			}}
			if manual {
				interaction.Input = make(chan string)
			}
			credential, err := (AuthClient{}).listenAndAuthorizeBrowser(t.Context(), interaction, occupied.Addr().String())
			if err == nil || !strings.Contains(err.Error(), "Device code login") || !strings.Contains(err.Error(), "cancel the other browser login") || credential.Configured() {
				t.Fatalf("missing actionable port-conflict error: %v", err)
			}
		})
	}
}

func TestBrowserLoginListenerReleasedBeforeRetry(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		notified := false
		_, err := (AuthClient{}).listenAndAuthorizeBrowser(ctx, LoginInteraction{Notify: func(_ context.Context, prompt LoginPrompt) error {
			notified = true
			parsed, _ := url.Parse(prompt.URL)
			if parsed.Query().Get("originator") != "aice" || parsed.Query().Get("redirect_uri") != redirectURI {
				t.Error("wrong browser authorization identity")
			}
			cancel()
			return nil
		}}, address)
		cancel()
		if !notified || !errors.Is(err, context.Canceled) {
			t.Fatalf("browser login retry failed: %v", err)
		}
	}
}
