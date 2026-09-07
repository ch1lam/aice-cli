package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

// Protocol reference: pi-mono 9767ba275f3e9a5ee0f5c5342249b629ab1b2282,
// packages/ai/src/auth/oauth/openai-codex.ts. This is an independent Go
// implementation of the OAuth exchange, not a port of its UI or server.
const (
	clientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	authBaseURL = "https://auth.openai.com"
	redirectURI = "http://localhost:1455/auth/callback"
)

// AuthClient owns bounded OAuth HTTP exchanges; zero values use OpenAI.
type AuthClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// LoginPrompt describes an authorization step without exposing any tokens.
type LoginPrompt struct {
	URL          string
	Code         string
	Instructions string
	AllowInput   bool
}

// LoginInteraction belongs to one login; Input can supply a pasted code or
// redirect URL while the loopback callback is also waiting.
type LoginInteraction struct {
	Notify func(context.Context, LoginPrompt) error
	Input  <-chan string
}

func writerInteraction(output io.Writer) LoginInteraction {
	return LoginInteraction{Notify: func(_ context.Context, prompt LoginPrompt) error {
		_, err := fmt.Fprintf(output, "%s\n%s\n", prompt.URL, prompt.Instructions)
		if err == nil && prompt.Code != "" {
			_, err = fmt.Fprintf(output, "Enter code: %s\n", prompt.Code)
		}
		return err
	}}
}

func (a AuthClient) baseURL() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return authBaseURL
}

func (a AuthClient) post(ctx context.Context, path, contentType string, body []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", contentType)
	client := &http.Client{}
	if a.HTTPClient != nil {
		*client = *a.HTTPClient
	}
	// Never follow redirects with refresh tokens or authorization codes.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("codex: authentication request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("codex: read authentication response: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, response.StatusCode, errors.New("codex: authentication response is too large")
	}
	return data, response.StatusCode, nil
}

func (a AuthClient) token(ctx context.Context, fields url.Values, previousRefresh string) (config.CodexCredentials, error) {
	fields.Set("client_id", clientID)
	data, status, err := a.post(ctx, "/oauth/token", "application/x-www-form-urlencoded", []byte(fields.Encode()))
	if err != nil {
		return config.CodexCredentials{}, err
	}
	if status != http.StatusOK {
		// Do not include response bodies: some servers echo submitted secrets.
		return config.CodexCredentials{}, fmt.Errorf("codex: token exchange failed (HTTP %d); run aice auth login --provider openai-codex", status)
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
	}
	if json.Unmarshal(data, &token) != nil || token.Access == "" || token.Expires <= 0 || token.Expires > 366*24*60*60 {
		return config.CodexCredentials{}, errors.New("codex: invalid token response")
	}
	if token.Refresh == "" {
		token.Refresh = previousRefresh
	}
	if token.Refresh == "" {
		return config.CodexCredentials{}, errors.New("codex: token response omitted refresh token")
	}
	account, err := accountID(token.Access)
	if err != nil {
		return config.CodexCredentials{}, err
	}
	return config.CodexCredentials{
		AccessToken: token.Access, RefreshToken: token.Refresh, AccountID: account,
		ExpiresAt: time.Now().Add(time.Duration(token.Expires) * time.Second),
	}, nil
}

// accountID reads routing metadata only. The access token is authenticated by
// OpenAI; local JWT decoding never grants authority or validates a signature.
func accountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("codex: invalid access token")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("codex: invalid access token payload")
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(data, &claims) != nil || strings.TrimSpace(claims.Auth.AccountID) == "" || strings.ContainsAny(claims.Auth.AccountID, "\r\n") {
		return "", errors.New("codex: access token has no valid ChatGPT account ID")
	}
	return claims.Auth.AccountID, nil
}

// Refresh renews a credential without starting an interactive login.
func (a AuthClient) Refresh(ctx context.Context, credential config.CodexCredentials) (config.CodexCredentials, error) {
	return a.token(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken},
	}, credential.RefreshToken)
}

// Login prints a browser URL or device code and waits for authorization.
// Cancellation and the fifteen-minute deadline own every wait and server.
func (a AuthClient) Login(ctx context.Context, device bool, output io.Writer) (config.CodexCredentials, error) {
	return a.LoginWithInteraction(ctx, device, writerInteraction(output))
}

// LoginWithInteraction supports CLI and TUI through the same OAuth lifecycle.
func (a AuthClient) LoginWithInteraction(ctx context.Context, device bool, interaction LoginInteraction) (config.CodexCredentials, error) {
	if interaction.Notify == nil {
		return config.CodexCredentials{}, errors.New("codex: login notifier is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if device {
		return a.deviceLogin(ctx, interaction)
	}
	return a.listenAndAuthorizeBrowser(ctx, interaction, "127.0.0.1:1455")
}

func (a AuthClient) listenAndAuthorizeBrowser(ctx context.Context, interaction LoginInteraction, address string) (config.CodexCredentials, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return config.CodexCredentials{}, ctx.Err()
		}
		// The fixed redirect would otherwise send this flow's callback to
		// another harness. Manual input does not make that collision safe.
		return config.CodexCredentials{}, fmt.Errorf("codex: cannot listen for browser login on %s (another login may be using it); cancel the other browser login and retry, or select Device code login (CLI: --device-code): %w", address, err)
	}
	return a.authorizeBrowser(ctx, interaction, listener)
}

func (a AuthClient) browserLogin(ctx context.Context, output io.Writer, listener net.Listener) (config.CodexCredentials, error) {
	return a.authorizeBrowser(ctx, writerInteraction(output), listener)
}

func (a AuthClient) authorizeBrowser(ctx context.Context, interaction LoginInteraction, listener net.Listener) (config.CodexCredentials, error) {
	verifier := randomURLToken()
	state := randomURLToken()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"scope":                 {"openid profile email offline_access"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"}, "state": {state},
		"id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}, "originator": {"aice"},
	}
	type callback struct {
		code string
		err  error
	}
	result := make(chan callback, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" {
				http.NotFound(w, r)
				return
			}
			values := r.URL.Query()
			if values.Get("state") != state {
				http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
				return
			}
			answer := callback{code: values.Get("code")}
			if values.Get("error") != "" {
				answer.err = errors.New("codex: browser authorization was denied")
			}
			if answer.code == "" && answer.err == nil {
				http.Error(w, "Missing authorization code", http.StatusBadRequest)
				return
			}
			select {
			case result <- answer:
			default:
			}
			_, _ = io.WriteString(w, "Authorization received. Return to AICE to check login status.")
		}),
	}
	var done chan struct{}
	var serveErr error
	if listener != nil {
		done = make(chan struct{})
		go func() { serveErr = server.Serve(listener); close(done) }()
		defer func() { _ = server.Close(); <-done }()
	}
	prompt := LoginPrompt{URL: a.baseURL() + "/oauth/authorize?" + query.Encode(), AllowInput: interaction.Input != nil,
		Instructions: "Complete login in your browser, or paste the authorization code / redirect URL here. Escape or Ctrl+C cancels."}
	if listener == nil {
		prompt.Instructions = "Callback port unavailable. Complete login in your browser, then paste the redirect URL here. Escape or Ctrl+C cancels."
	}
	if err := interaction.Notify(ctx, prompt); err != nil {
		return config.CodexCredentials{}, err
	}
	input := interaction.Input
	for {
		var code string
		select {
		case <-done:
			return config.CodexCredentials{}, fmt.Errorf("codex: OAuth callback server: %w", serveErr)
		case <-ctx.Done():
			return config.CodexCredentials{}, ctx.Err()
		case answer := <-result:
			if answer.err != nil {
				return config.CodexCredentials{}, answer.err
			}
			code = answer.code
		case value, open := <-input:
			if !open {
				input = nil
				if listener == nil {
					return config.CodexCredentials{}, errors.New("codex: authorization input closed")
				}
				continue
			}
			var err error
			code, err = authorizationCode(value, state)
			if err != nil {
				prompt.Instructions = err.Error() + ". Paste the redirect URL from this login, or complete the browser callback."
				if err := interaction.Notify(ctx, prompt); err != nil {
					return config.CodexCredentials{}, err
				}
				continue
			}
		}
		return a.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}}, "")
	}
}

func authorizationCode(value, state string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64*1024 {
		return "", errors.New("codex: missing or oversized authorization input")
	}
	var code, suppliedState string
	switch {
	case strings.Contains(value, "://"):
		parsed, err := url.Parse(value)
		if err != nil {
			return "", errors.New("codex: invalid redirect URL")
		}
		code, suppliedState = parsed.Query().Get("code"), parsed.Query().Get("state")
	case strings.Contains(value, "code="):
		parsed, err := url.ParseQuery(strings.TrimPrefix(value, "?"))
		if err != nil {
			return "", errors.New("codex: invalid authorization query")
		}
		code, suppliedState = parsed.Get("code"), parsed.Get("state")
	case strings.Contains(value, "#"):
		code, suppliedState, _ = strings.Cut(value, "#")
	default:
		// A bare code is still bound to this flow by its private PKCE verifier.
		code, suppliedState = value, state
	}
	if suppliedState != state {
		return "", errors.New("codex: OAuth state mismatch")
	}
	if code == "" || strings.ContainsAny(code, " \t\r\n") {
		return "", errors.New("codex: invalid authorization code")
	}
	return code, nil
}

func randomURLToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:]) // Go's crypto/rand.Read always fills the buffer.
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func (a AuthClient) deviceLogin(ctx context.Context, interaction LoginInteraction) (config.CodexCredentials, error) {
	data, status, err := a.post(ctx, "/api/accounts/deviceauth/usercode", "application/json", []byte(`{"client_id":"`+clientID+`"}`))
	if err != nil {
		return config.CodexCredentials{}, err
	}
	if status != http.StatusOK {
		return config.CodexCredentials{}, fmt.Errorf("codex: device login unavailable (HTTP %d); enable device code login in ChatGPT security settings or use browser login", status)
	}
	var device struct {
		ID       string          `json:"device_auth_id"`
		Code     string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	if json.Unmarshal(data, &device) != nil || device.ID == "" || device.Code == "" {
		return config.CodexCredentials{}, errors.New("codex: invalid device code response")
	}
	seconds, err := strconv.ParseFloat(strings.Trim(string(device.Interval), `"`), 64)
	if err != nil || !(seconds >= 0 && seconds <= 900) {
		return config.CodexCredentials{}, errors.New("codex: invalid device polling interval")
	}
	interval := max(time.Second, time.Duration(seconds*float64(time.Second)))
	if err := interaction.Notify(ctx, LoginPrompt{URL: a.baseURL() + "/codex/device", Code: device.Code,
		Instructions: "Enter the code in your browser. Waiting for authentication... Escape or Ctrl+C cancels."}); err != nil {
		return config.CodexCredentials{}, err
	}
	body, err := json.Marshal(map[string]string{"device_auth_id": device.ID, "user_code": device.Code})
	if err != nil {
		return config.CodexCredentials{}, err
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return config.CodexCredentials{}, ctx.Err()
		case <-timer.C:
		}
		data, status, err := a.post(ctx, "/api/accounts/deviceauth/token", "application/json", body)
		if err != nil {
			return config.CodexCredentials{}, err
		}
		if status == http.StatusForbidden || status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK {
			var failure struct {
				Error json.RawMessage `json:"error"`
			}
			_ = json.Unmarshal(data, &failure)
			var code string
			if json.Unmarshal(failure.Error, &code) != nil {
				var nested struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(failure.Error, &nested)
				code = nested.Code
			}
			if code == "deviceauth_authorization_pending" {
				continue
			}
			if code == "slow_down" {
				interval += 5 * time.Second
				continue
			}
			return config.CodexCredentials{}, fmt.Errorf("codex: device authorization failed (HTTP %d)", status)
		}
		var answer struct {
			Code     string `json:"authorization_code"`
			Verifier string `json:"code_verifier"`
		}
		if json.Unmarshal(data, &answer) != nil || answer.Code == "" || answer.Verifier == "" {
			return config.CodexCredentials{}, errors.New("codex: invalid device authorization response")
		}
		return a.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {answer.Code}, "code_verifier": {answer.Verifier}, "redirect_uri": {a.baseURL() + "/deviceauth/callback"}}, "")
	}
}
