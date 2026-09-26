package claudesubscription

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
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

// Protocol reference: pi-mono d6af72e1857cfb10b41d8ff8e69f0d72b4cf6d31,
// packages/ai/src/auth/oauth/anthropic.ts. The lifecycle is AICE's own Go
// implementation; no upstream source or runtime is embedded.
const (
	clientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	authorizeURL = "https://claude.ai/oauth/authorize"
	tokenURL     = "https://platform.claude.com/v1/oauth/token"
	redirectURI  = "http://localhost:53692/callback"
	scopes       = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

// AuthClient owns bounded OAuth exchanges; zero values use Anthropic.
// Endpoint overrides are constructor-only seams for offline tests.
type AuthClient struct {
	AuthorizeURL string
	TokenURL     string
	HTTPClient   *http.Client
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

func (a AuthClient) authorizationURL() string {
	if a.AuthorizeURL != "" {
		return a.AuthorizeURL
	}
	return authorizeURL
}

func (a AuthClient) post(ctx context.Context, body []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := a.TokenURL
	if endpoint == "" {
		endpoint = tokenURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{}
	if a.HTTPClient != nil {
		*client = *a.HTTPClient
	}
	// Never follow redirects with refresh tokens or authorization codes.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("claude-subscription: authentication request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("claude-subscription: read authentication response: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, response.StatusCode, errors.New("claude-subscription: authentication response is too large")
	}
	return data, response.StatusCode, nil
}

func (a AuthClient) token(ctx context.Context, fields map[string]string, previousRefresh string) (config.ClaudeSubscriptionCredentials, error) {
	fields["client_id"] = clientID
	body, err := json.Marshal(fields)
	if err != nil {
		return config.ClaudeSubscriptionCredentials{}, err
	}
	data, status, err := a.post(ctx, body)
	if err != nil {
		return config.ClaudeSubscriptionCredentials{}, err
	}
	if status != http.StatusOK {
		// Do not include response bodies: some servers echo submitted secrets.
		return config.ClaudeSubscriptionCredentials{}, fmt.Errorf("claude-subscription: token exchange failed (HTTP %d); run aice auth login --provider anthropic-subscription", status)
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
	}
	if json.Unmarshal(data, &token) != nil || strings.TrimSpace(token.Access) == "" || strings.ContainsAny(token.Access+token.Refresh, "\r\n") || token.Expires <= 0 || token.Expires > 366*24*60*60 {
		return config.ClaudeSubscriptionCredentials{}, errors.New("claude-subscription: invalid token response")
	}
	if token.Refresh == "" {
		token.Refresh = previousRefresh
	}
	if strings.TrimSpace(token.Refresh) == "" {
		return config.ClaudeSubscriptionCredentials{}, errors.New("claude-subscription: token response omitted refresh token")
	}
	return config.ClaudeSubscriptionCredentials{
		AccessToken: token.Access, RefreshToken: token.Refresh,
		ExpiresAt: time.Now().Add(time.Duration(token.Expires) * time.Second),
	}, nil
}

// Refresh renews a credential without starting an interactive login.
func (a AuthClient) Refresh(ctx context.Context, credential config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
	return a.token(ctx, map[string]string{
		"grant_type": "refresh_token", "refresh_token": credential.RefreshToken,
	}, credential.RefreshToken)
}

// Login prints a browser URL and waits for authorization.
// Cancellation and the fifteen-minute deadline own every wait and server.
func (a AuthClient) Login(ctx context.Context, output io.Writer) (config.ClaudeSubscriptionCredentials, error) {
	return a.LoginWithInteraction(ctx, writerInteraction(output))
}

// LoginWithInteraction supports CLI and TUI through the same OAuth lifecycle.
func (a AuthClient) LoginWithInteraction(ctx context.Context, interaction LoginInteraction) (config.ClaudeSubscriptionCredentials, error) {
	if interaction.Notify == nil {
		return config.ClaudeSubscriptionCredentials{}, errors.New("claude-subscription: login notifier is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	return a.listenAndAuthorizeBrowser(ctx, interaction, "127.0.0.1:53692")
}

func (a AuthClient) listenAndAuthorizeBrowser(ctx context.Context, interaction LoginInteraction, address string) (config.ClaudeSubscriptionCredentials, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return config.ClaudeSubscriptionCredentials{}, ctx.Err()
		}
		// The fixed redirect would otherwise send this flow's callback to
		// another harness. Manual input does not make that collision safe.
		return config.ClaudeSubscriptionCredentials{}, fmt.Errorf("claude-subscription: cannot listen for browser login on %s (another login may be using it); cancel the other browser login and retry: %w", address, err)
	}
	return a.authorizeBrowser(ctx, interaction, listener)
}

func (a AuthClient) authorizeBrowser(ctx context.Context, interaction LoginInteraction, listener net.Listener) (config.ClaudeSubscriptionCredentials, error) {
	verifier := randomURLToken()
	state := randomURLToken()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"scope":                 {scopes},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"}, "state": {state},
		"code": {"true"},
	}
	type callback struct {
		code string
		err  error
	}
	result := make(chan callback, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet || r.URL.Path != "/callback" {
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
				answer.err = errors.New("claude-subscription: browser authorization was denied")
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
	prompt := LoginPrompt{URL: a.authorizationURL() + "?" + query.Encode(), AllowInput: interaction.Input != nil,
		Instructions: "Complete login in your browser, or paste the authorization code / redirect URL here. Escape or Ctrl+C cancels."}
	if listener == nil {
		prompt.Instructions = "Callback port unavailable. Complete login in your browser, then paste the redirect URL here. Escape or Ctrl+C cancels."
	}
	if err := interaction.Notify(ctx, prompt); err != nil {
		return config.ClaudeSubscriptionCredentials{}, err
	}
	input := interaction.Input
	for {
		var code string
		select {
		case <-done:
			return config.ClaudeSubscriptionCredentials{}, fmt.Errorf("claude-subscription: OAuth callback server: %w", serveErr)
		case <-ctx.Done():
			return config.ClaudeSubscriptionCredentials{}, ctx.Err()
		case answer := <-result:
			if answer.err != nil {
				return config.ClaudeSubscriptionCredentials{}, answer.err
			}
			code = answer.code
		case value, open := <-input:
			if !open {
				input = nil
				if listener == nil {
					return config.ClaudeSubscriptionCredentials{}, errors.New("claude-subscription: authorization input closed")
				}
				continue
			}
			var err error
			code, err = authorizationCode(value, state)
			if err != nil {
				prompt.Instructions = err.Error() + ". Paste the redirect URL from this login, or complete the browser callback."
				if err := interaction.Notify(ctx, prompt); err != nil {
					return config.ClaudeSubscriptionCredentials{}, err
				}
				continue
			}
		}
		return a.token(ctx, map[string]string{"grant_type": "authorization_code", "code": code, "state": state, "code_verifier": verifier, "redirect_uri": redirectURI}, "")
	}
}

func authorizationCode(value, state string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64*1024 {
		return "", errors.New("claude-subscription: missing or oversized authorization input")
	}
	var code, suppliedState string
	switch {
	case strings.Contains(value, "://"):
		parsed, err := url.Parse(value)
		if err != nil {
			return "", errors.New("claude-subscription: invalid redirect URL")
		}
		code, suppliedState = parsed.Query().Get("code"), parsed.Query().Get("state")
	case strings.Contains(value, "code="):
		parsed, err := url.ParseQuery(strings.TrimPrefix(value, "?"))
		if err != nil {
			return "", errors.New("claude-subscription: invalid authorization query")
		}
		code, suppliedState = parsed.Get("code"), parsed.Get("state")
	case strings.Contains(value, "#"):
		code, suppliedState, _ = strings.Cut(value, "#")
	default:
		// A bare code is still bound to this flow by its private PKCE verifier.
		code, suppliedState = value, state
	}
	if suppliedState != state {
		return "", errors.New("claude-subscription: OAuth state mismatch")
	}
	if code == "" || strings.ContainsAny(code, " \t\r\n") {
		return "", errors.New("claude-subscription: invalid authorization code")
	}
	return code, nil
}

func randomURLToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:]) // Go's crypto/rand.Read always fills the buffer.
	return base64.RawURLEncoding.EncodeToString(data[:])
}
