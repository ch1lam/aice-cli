package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/mcpauth"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type mcpRefreshFunc func(context.Context, config.MCPServer) (config.MCPOAuthCredentials, error)

// mcpOAuthRefresh freezes the auth-file location, not the rotating token. The
// existing writer rereads and verifies the grant under its cross-process lock.
// Rediscovery must match the saved binding before any token is sent. It never
// prompts, reconnects, or replays an RPC.
func mcpOAuthRefresh(paths config.Paths) mcpRefreshFunc {
	return func(ctx context.Context, server config.MCPServer) (config.MCPOAuthCredentials, error) {
		credential, _, err := config.RefreshMCPOAuth(ctx, paths, server, func(ctx context.Context, current config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
			if current.ExpiresAt.IsZero() || current.ExpiresAt.After(time.Now()) {
				return current, nil
			}
			if current.RefreshToken == "" {
				return config.MCPOAuthCredentials{}, fmt.Errorf("MCP OAuth refresh token is unavailable")
			}
			binding := mcpauth.Binding{Resource: current.Resource, Issuer: current.Issuer, TokenEndpoint: current.TokenEndpoint}
			registration := mcpauth.Registration{Binding: binding, ClientID: current.ClientID, ClientSecret: current.ClientSecret, AuthMethod: current.AuthMethod, RedirectURI: current.RedirectURI}
			previous := mcpauth.Token{Binding: binding, ClientID: current.ClientID, AccessToken: current.AccessToken, RefreshToken: current.RefreshToken, ExpiresAt: current.ExpiresAt, Scopes: current.Scopes}
			client := mcpauth.Client{}
			discovery, err := client.Probe(ctx, current.Resource)
			if err != nil {
				return config.MCPOAuthCredentials{}, err
			}
			discovery.Issuer = current.Issuer
			authority, err := client.Discover(ctx, discovery)
			if err != nil {
				return config.MCPOAuthCredentials{}, err
			}
			next, err := client.Refresh(ctx, authority, registration, previous)
			if err != nil {
				return config.MCPOAuthCredentials{}, err
			}
			current.AccessToken, current.RefreshToken, current.ExpiresAt, current.Scopes = next.AccessToken, next.RefreshToken, next.ExpiresAt, next.Scopes
			return current, nil
		})
		return credential, err
	}
}

// prepareAuthentication runs with the service gate held, before entering the
// transport queue. It releases the owner mutex for disk/network I/O. The final
// dispatch callback remains local: later expiry rejects dispatch without replay.
// A failed refresh stops this owner's automatic attempts: the server may already
// have consumed a refresh token even if its response or durable save was lost.
func (o *mcpOwner) prepareAuthentication(ctx context.Context, s *mcpOwnedService) error {
	o.mu.Lock()
	if err := ctx.Err(); err != nil {
		o.mu.Unlock()
		return err
	}
	if err := o.policyLocked(s); err != nil {
		o.mu.Unlock()
		return err
	}
	if !s.server.OAuthTokenExpired(time.Now()) {
		o.mu.Unlock()
		return nil
	}
	if o.refresh == nil {
		err := o.permissionLocked(s)
		o.mu.Unlock()
		return err
	}
	// Retain every token seen by this owner for late/concurrent result mapping.
	// Reserve both maximum-size rotated tokens before permitting an exchange.
	bytes := 0
	for _, secret := range s.secrets {
		bytes += len(secret)
	}
	if len(s.secrets) > 4094 || bytes > (4<<20)-(16<<10) {
		s.authFailed = true
		s.status.State, s.status.Detail = "needs_auth", "MCP credential redaction history is full; explicitly reconnect before refreshing"
		o.mu.Unlock()
		return fmt.Errorf("MCP credential redaction history limit reached")
	}
	server := s.server
	o.mu.Unlock()
	credential, err := o.refresh(ctx, server)
	var updated config.MCPServer
	if err == nil {
		updated, err = server.WithRefreshedMCPOAuth(credential)
	}
	if err == nil && updated.OAuthTokenExpired(time.Now()) {
		err = fmt.Errorf("MCP refresh returned an expired token")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// Record valid returned credentials even when cancellation/revocation prevents
	// publication. Results already in flight still need credential redaction.
	if err == nil {
		for _, secret := range []string{credential.AccessToken, credential.RefreshToken} {
			if secret != "" && !slices.Contains(s.secrets, secret) {
				s.secrets = append(s.secrets, secret)
			}
		}
	}
	if err != nil {
		s.authFailed = true
		_ = o.policyLocked(s)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MCP OAuth refresh failed; inspect credentials and explicitly reconnect or log in")
	}
	if err := o.policyLocked(s); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.server = updated
	s.status.State, s.status.Detail = "disconnected", ""
	if s.client != nil {
		s.status.State = "ready"
	}
	return nil
}

func (o *mcpOwner) recordAuthenticationFailure(s *mcpOwnedService, err error) {
	if !mcpAuthenticationRejected(err) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s.authFailed = s.server.Settings.OAuth != nil
	s.status.State, s.status.Detail = "needs_auth", "MCP authentication was rejected; no operation was replayed"
}

func (b mcpBorrowedConnection) MCPSecrets() []string {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	return slices.Clone(b.service.secrets)
}

func mcpAuthenticationRejected(err error) bool {
	var status *mcpclient.HTTPError
	return errors.As(err, &status) && (status.StatusCode == 401 || status.StatusCode == 403)
}
