package config

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeSubscriptionCredentials belongs to AICE, independently of other harnesses' tokens.
type ClaudeSubscriptionCredentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Configured includes expired access tokens that can still be refreshed.
func (c ClaudeSubscriptionCredentials) Configured() bool {
	return strings.TrimSpace(c.AccessToken) != "" && strings.TrimSpace(c.RefreshToken) != "" &&
		!strings.ContainsAny(c.AccessToken+c.RefreshToken, "\r\n") && !c.ExpiresAt.IsZero()
}

// ClaudeSubscriptionAuthPath keeps rotating OAuth credentials separate from API key writes.
func ClaudeSubscriptionAuthPath(paths Paths) string {
	return filepath.Join(filepath.Dir(paths.GlobalAuth), "claude-subscription-auth.json")
}

// LoadClaudeSubscriptionCredentials reads only AICE's explicit global credential path.
func LoadClaudeSubscriptionCredentials(paths Paths) (ClaudeSubscriptionCredentials, error) {
	return loadCredentialFile[ClaudeSubscriptionCredentials](paths, ClaudeSubscriptionAuthPath(paths), "Claude subscription")
}

// UpdateClaudeSubscriptionCredentials serializes refresh, login, and logout across AICE
// processes. The callback receives the latest disk value under the lock.
func UpdateClaudeSubscriptionCredentials(ctx context.Context, paths Paths,
	update func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error),
) (ClaudeSubscriptionCredentials, error) {
	return updateCredentialFile(ctx, paths, ClaudeSubscriptionAuthPath(paths), "Claude subscription", func(credential ClaudeSubscriptionCredentials) bool {
		return credential.Configured()
	}, update)
}
