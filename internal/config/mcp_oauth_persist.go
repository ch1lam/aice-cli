package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"
)

// SaveMCPOAuthLogin creates a fresh grant identity after successful consent.
// Login never inherits connection approval, even for the same remote account.
func SaveMCPOAuthLogin(ctx context.Context, paths Paths, server MCPServer, credential MCPOAuthCredentials) (MCPOAuthCredentials, CommitResult, error) {
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return MCPOAuthCredentials{}, CommitResult{}, err
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	credential.GrantID = hex.EncodeToString(id)
	if !server.acceptsOAuth(credential) {
		return MCPOAuthCredentials{}, CommitResult{}, fmt.Errorf("config: invalid MCP OAuth login binding")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	commit, err := editLockedFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		records, err := readMCPOAuthValues(values)
		if err != nil {
			return err
		}
		// Only the current scope's fresh grant remains usable after this login.
		records[server.Key] = map[string]MCPOAuthCredentials{server.CredentialScope: credential.clone()}
		if _, err := decodeMCPOAuth(records); err != nil {
			return err
		}
		if err := clearMCPOAuthApproval(values, server.Key); err != nil {
			return err
		}
		values[mcpOAuthKey] = records
		return nil
	})
	if !commit.Committed {
		return MCPOAuthCredentials{}, commit, err
	}
	return credential.clone(), commit, err
}

// RefreshMCPOAuth rereads the current grant while holding the shared auth-file
// lock. The callback must check expiry before exchanging and honor its context.
// Concurrent processes see the preceding rotation; a removed/replaced grant
// cannot be resurrected by an old owner. No HTTP call is retried here.
func RefreshMCPOAuth(ctx context.Context, paths Paths, server MCPServer,
	refresh func(context.Context, MCPOAuthCredentials) (MCPOAuthCredentials, error),
) (MCPOAuthCredentials, CommitResult, error) {
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return MCPOAuthCredentials{}, CommitResult{}, err
	}
	expected, ok := server.OAuthCredentials()
	if !ok || refresh == nil {
		return MCPOAuthCredentials{}, CommitResult{}, fmt.Errorf("config: MCP OAuth login is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var current MCPOAuthCredentials
	commit, err := editLockedFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		records, err := readMCPOAuthValues(values)
		if err != nil {
			return err
		}
		previous := records[server.Key][server.CredentialScope]
		if !sameMCPOAuthGrant(previous, expected) || !server.acceptsOAuth(previous) {
			return fmt.Errorf("MCP OAuth grant was removed or changed; login and approval are required")
		}
		next, err := refresh(ctx, previous.clone())
		if err != nil {
			return err
		}
		if !sameMCPOAuthGrant(next, previous) || !server.acceptsOAuth(next) {
			return fmt.Errorf("MCP OAuth refresh changed the login identity")
		}
		current = next.clone()
		if reflect.DeepEqual(previous, next) {
			return errConfigUnchanged
		}
		records[server.Key][server.CredentialScope] = next.clone()
		if _, err := decodeMCPOAuth(records); err != nil {
			return err
		}
		values[mcpOAuthKey] = records
		return nil
	})
	if err != nil && !commit.Committed {
		return MCPOAuthCredentials{}, commit, err
	}
	return current, commit, err
}

func sameMCPOAuthGrant(a, b MCPOAuthCredentials) bool {
	return a.GrantID != "" && a.identity() == b.identity()
}

// DeleteMCPOAuth removes OAuth state and its connection decision atomically.
// Explicit header/env slots and all other services/providers are preserved.
func DeleteMCPOAuth(ctx context.Context, paths Paths, server MCPServer) (CommitResult, error) {
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return CommitResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return editLockedFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		records, err := readMCPOAuthValues(values)
		if err != nil {
			return err
		}
		delete(records, server.Key)
		if err := clearMCPOAuthApproval(values, server.Key); err != nil {
			return err
		}
		values[mcpOAuthKey] = records
		return nil
	})
}

func readMCPOAuthValues(values map[string]any) (mcpOAuthCredentials, error) {
	if raw, ok := values[mcpOAuthKey]; ok {
		return decodeMCPOAuth(raw)
	}
	return make(mcpOAuthCredentials), nil
}
func clearMCPOAuthApproval(values map[string]any, key string) error {
	if err := clearMCPPermissions(values, key); err != nil {
		return err
	}
	if raw, ok := values[mcpConnectionsKey]; ok {
		approvals, err := decodeMCPConnections(raw)
		if err != nil {
			return err
		}
		delete(approvals, key)
		values[mcpConnectionsKey] = approvals
	}
	return nil
}
