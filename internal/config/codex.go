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
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return CodexCredentials{}, errors.New("config: global auth path is required")
	}
	data, err := os.ReadFile(CodexAuthPath(paths))
	if errors.Is(err, os.ErrNotExist) {
		return CodexCredentials{}, nil
	}
	if err != nil {
		return CodexCredentials{}, fmt.Errorf("config: read Codex credentials: %w", err)
	}
	var credential CodexCredentials
	if json.Unmarshal(data, &credential) != nil {
		return CodexCredentials{}, fmt.Errorf("config: invalid Codex credential file; remove %s and run aice auth login", CodexAuthPath(paths))
	}
	return credential, nil
}

// UpdateCodexCredentials serializes refresh, login, and logout across AICE
// processes. The callback receives the latest disk value under the lock.
// A crashed lock is never stolen while its owner might still be refreshing.
func UpdateCodexCredentials(ctx context.Context, paths Paths,
	update func(CodexCredentials) (CodexCredentials, error),
) (credential CodexCredentials, returnErr error) {
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return credential, errors.New("config: global auth path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	path := CodexAuthPath(paths)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return credential, fmt.Errorf("config: create Codex auth directory: %w", err)
	}
	lock := path + ".lock"
	if err := acquireCodexCredentialLock(ctx, lock, os.Mkdir, runtime.GOOS == "windows"); err != nil {
		return credential, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.Remove(lock)) }()
	previous, err := LoadCodexCredentials(paths)
	if err != nil {
		return credential, err
	}
	credential, err = update(previous)
	if err != nil {
		return CodexCredentials{}, err
	}
	if credential == previous {
		return credential, nil
	}
	if credential == (CodexCredentials{}) {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return credential, err
	}
	if !credential.Configured() || strings.ContainsAny(credential.AccessToken+credential.RefreshToken+credential.AccountID, "\r\n") {
		return CodexCredentials{}, errors.New("config: incomplete or invalid Codex credentials")
	}
	data, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return CodexCredentials{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".codex-auth-*")
	if err != nil {
		return CodexCredentials{}, err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return CodexCredentials{}, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return CodexCredentials{}, err
	}
	return credential, nil
}

// acquireCodexCredentialLock keeps filesystem attempts injectable so retry
// behavior can be tested without relying on OS-specific deletion timing.
// The caller supplies the wait deadline and releases only an acquired lock.
func acquireCodexCredentialLock(ctx context.Context, lock string,
	mkdir func(string, os.FileMode) error, retryPermission bool,
) error {
	var lastLockErr error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("config: wait for %s (check permissions; remove a stale lock only when no AICE process is running): %w", lock, errors.Join(err, lastLockErr))
		}
		err := mkdir(lock, 0o700)
		if err == nil {
			return nil
		}
		// Windows can deny creation while the previous lock directory is
		// pending deletion. Retry within the same bounded, cancellable wait;
		// only a successful Mkdir grants ownership of the lock.
		if !errors.Is(err, os.ErrExist) && !(retryPermission && errors.Is(err, os.ErrPermission)) {
			return fmt.Errorf("config: lock Codex credentials: %w", err)
		}
		lastLockErr = err
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
