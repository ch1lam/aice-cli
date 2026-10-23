package config

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// CodexCredentials belongs to AICE, independently of other harnesses' tokens.
type CodexCredentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Configured includes expired access tokens that can still be refreshed.
func (c CodexCredentials) Configured() bool {
	return c.AccessToken != "" && c.RefreshToken != "" && c.AccountID != "" && !c.ExpiresAt.IsZero()
}

// CodexAuthPath keeps rotating OAuth credentials separate from API key writes.
func CodexAuthPath(paths Paths) string {
	return filepath.Join(filepath.Dir(paths.GlobalAuth), "codex-auth.json")
}

// LoadCodexCredentials reads only AICE's explicit global credential path.
func LoadCodexCredentials(paths Paths) (CodexCredentials, error) {
	return loadCredentialFile[CodexCredentials](paths, CodexAuthPath(paths), "Codex")
}

// UpdateCodexCredentials serializes refresh, login, and logout across AICE
// processes. The callback receives the latest disk value under the lock.
func UpdateCodexCredentials(ctx context.Context, paths Paths,
	update func(CodexCredentials) (CodexCredentials, error),
) (CodexCredentials, error) {
	return updateCredentialFile(ctx, paths, CodexAuthPath(paths), "Codex", func(credential CodexCredentials) bool {
		return credential.Configured() && !strings.ContainsAny(credential.AccessToken+credential.RefreshToken+credential.AccountID, "\r\n")
	}, update)
}
