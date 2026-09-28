package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/mcpauth"
)

type mcpLoginFunc func(context.Context, config.MCPServer) (config.MCPOAuthCredentials, error)

func (a *application) mcpLogin(ui *interaction.AuthInteraction, noBrowser bool) mcpLoginFunc {
	return func(ctx context.Context, server config.MCPServer) (config.MCPOAuthCredentials, error) {
		var open func(context.Context, string) error
		if a != nil && !noBrowser {
			open = a.dependencies.openBrowser
		}
		return loginMCPOAuth(ctx, server, ui, open)
	}
}

// loginMCPOAuth owns one listener and authorization attempt. Browser callbacks
// and optional manual input share the same one-time exchange. No credential is
// persisted until the management coordinator receives a successful result.
func loginMCPOAuth(ctx context.Context, server config.MCPServer, ui *interaction.AuthInteraction, open func(context.Context, string) error) (config.MCPOAuthCredentials, error) {
	if ui == nil || ui.Notify == nil || server.Settings.OAuth == nil {
		return config.MCPOAuthCredentials{}, fmt.Errorf("MCP OAuth login requires configuration and a transient interaction")
	}
	if err := (config.MCPSettings{Servers: map[string]config.MCPServerSettings{server.ID: server.Settings}}).Validate(); err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	settings := server.Settings.OAuth
	if settings.ClientSecret != nil && server.OAuthClientSecret() == "" {
		return config.MCPOAuthCredentials{}, fmt.Errorf("MCP OAuth client secret is missing; set its referenced credential first")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	client := mcpauth.Client{}
	discovery, err := client.Probe(ctx, server.Settings.URL)
	if err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	discovery.Issuer = settings.Issuer
	if settings.Scopes != nil {
		discovery.Scopes = append([]string{}, (*settings.Scopes)...)
	}
	authority, err := client.Discover(ctx, discovery)
	if err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	redirect := settings.RedirectURI
	address := "127.0.0.1:0"
	if redirect != "" {
		u, _ := url.Parse(redirect)
		address = u.Host
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return config.MCPOAuthCredentials{}, fmt.Errorf("MCP OAuth callback port is unavailable")
	}
	defer listener.Close()
	if redirect == "" {
		redirect = "http://" + listener.Addr().String() + "/oauth/callback"
	}
	var registration mcpauth.Registration
	if settings.ClientID != "" {
		registration, err = authority.PreRegistered(settings.ClientID, server.OAuthClientSecret(), settings.Method(), redirect)
	} else {
		registration, err = client.Register(ctx, authority, redirect)
	}
	if err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	attempt, err := authority.Begin(registration)
	if err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	callbackURL, _ := url.Parse(redirect)
	callbacks := make(chan string, 4)
	httpServer := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			if r.Method != http.MethodGet || r.Host != callbackURL.Host || r.URL.EscapedPath() != callbackURL.EscapedPath() || r.URL.IsAbs() || len(r.RequestURI) > 8192 {
				http.Error(w, "Invalid OAuth callback", http.StatusBadRequest)
				return
			}
			select {
			case callbacks <- "http://" + r.Host + r.RequestURI:
				_, _ = fmt.Fprintln(w, "Callback received. Check AICE for the authorization result.")
			default:
				http.Error(w, "Callback queue is full; retry in AICE", http.StatusTooManyRequests)
			}
		}),
	}
	serveDone := make(chan error, 1)
	go func() { defer close(serveDone); serveDone <- httpServer.Serve(listener) }()
	defer func() { _ = httpServer.Close(); <-serveDone }()
	prompt := interaction.AuthPrompt{Title: "Log in to MCP: " + server.Key, URL: attempt.AuthorizationURL(), AllowInput: ui.Input != nil,
		Instructions: "Authorize this service in your browser. The loopback callback completes login. In the interactive prompt you may paste the complete redirect URL. Cancel to stop; login does not grant connection or tool permission."}
	if err := ui.Notify(ctx, prompt); err != nil {
		return config.MCPOAuthCredentials{}, err
	}
	if open != nil {
		if err := open(ctx, prompt.URL); err != nil {
			if ctx.Err() != nil {
				return config.MCPOAuthCredentials{}, ctx.Err()
			}
			prompt.Instructions = "Could not open the browser. Open the displayed authorization URL manually; the loopback callback must reach this AICE process."
			if err := ui.Notify(ctx, prompt); err != nil {
				return config.MCPOAuthCredentials{}, err
			}
		}
	}
	for {
		var callback string
		select {
		case <-ctx.Done():
			return config.MCPOAuthCredentials{}, ctx.Err()
		case <-serveDone:
			return config.MCPOAuthCredentials{}, fmt.Errorf("MCP OAuth callback listener stopped")
		case value := <-callbacks:
			callback = value
		case value, ok := <-ui.Input:
			if !ok {
				return config.MCPOAuthCredentials{}, context.Canceled
			}
			callback = value
		}
		token, err := client.Complete(ctx, attempt, callback)
		if errors.Is(err, mcpauth.ErrCallback) {
			prompt.Instructions = "Invalid OAuth callback. Complete the same browser authorization or paste its full redirect URL; no token exchange was sent."
			if err := ui.Notify(ctx, prompt); err != nil {
				return config.MCPOAuthCredentials{}, err
			}
			continue
		}
		if err != nil {
			return config.MCPOAuthCredentials{}, err
		}
		return config.MCPOAuthCredentials{Resource: token.Binding.Resource, Issuer: token.Binding.Issuer, TokenEndpoint: token.Binding.TokenEndpoint,
			ClientID: registration.ClientID, ClientSecret: registration.ClientSecret, AuthMethod: registration.AuthMethod, RedirectURI: registration.RedirectURI,
			AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt, Scopes: token.Scopes}, nil
	}
}
