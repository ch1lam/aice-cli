package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return ClaudeSubscriptionCredentials{}, errors.New("config: global auth path is required")
	}
	data, err := os.ReadFile(ClaudeSubscriptionAuthPath(paths))
	if errors.Is(err, os.ErrNotExist) {
		return ClaudeSubscriptionCredentials{}, nil
	}
	if err != nil {
		return ClaudeSubscriptionCredentials{}, fmt.Errorf("config: read Claude subscription credentials: %w", err)
	}
	var credential ClaudeSubscriptionCredentials
	if json.Unmarshal(data, &credential) != nil {
		return ClaudeSubscriptionCredentials{}, &sourceSyntaxError{path: ClaudeSubscriptionAuthPath(paths)}
	}
	return credential, nil
}

// UpdateClaudeSubscriptionCredentials serializes refresh, login, and logout across AICE
// processes. The callback receives the latest disk value under the lock.
// A crashed lock is never stolen while its owner might still be refreshing.
func UpdateClaudeSubscriptionCredentials(ctx context.Context, paths Paths,
	update func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error),
) (credential ClaudeSubscriptionCredentials, returnErr error) {
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return credential, errors.New("config: global auth path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	path := ClaudeSubscriptionAuthPath(paths)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return credential, fmt.Errorf("config: create Claude subscription auth directory: %w", err)
	}
	lock := path + ".lock"
	if err := acquireConfigLock(ctx, lock, os.Mkdir, runtime.GOOS == "windows"); err != nil {
		return credential, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.Remove(lock)) }()
	previous, err := LoadClaudeSubscriptionCredentials(paths)
	if err != nil {
		return credential, err
	}
	credential, err = update(previous)
	if err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	if credential == previous {
		return credential, nil
	}
	if credential == (ClaudeSubscriptionCredentials{}) {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return credential, err
	}
	if !credential.Configured() || strings.ContainsAny(credential.AccessToken+credential.RefreshToken, "\r\n") {
		return ClaudeSubscriptionCredentials{}, errors.New("config: incomplete or invalid Claude subscription credentials")
	}
	data, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".claude-subscription-auth-*")
	if err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	if err := renameConfigFile(ctx, file.Name(), path, os.Rename, runtime.GOOS == "windows"); err != nil {
		return ClaudeSubscriptionCredentials{}, err
	}
	return credential, nil
}
