package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/lockedfile"
)

// loadCredentialFile reads one rotating OAuth credential file owned by AICE.
// A missing file is the zero credential.
func loadCredentialFile[T any](paths Paths, path, label string) (T, error) {
	var credential T
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return credential, errors.New("config: global auth path is required")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return credential, nil
	}
	if err != nil {
		return credential, fmt.Errorf("config: read %s credentials: %w", label, err)
	}
	if json.Unmarshal(data, &credential) != nil {
		return credential, &sourceSyntaxError{path: path}
	}
	return credential, nil
}

// updateCredentialFile serializes refresh, login and logout across AICE
// processes. update receives the latest disk value under the lock and may
// return the zero credential to delete the file. The lock is held for at most
// one minute, long enough for a bounded token exchange.
func updateCredentialFile[T comparable](ctx context.Context, paths Paths, path, label string,
	valid func(T) bool, update func(T) (T, error),
) (credential T, returnErr error) {
	var zero T
	if strings.TrimSpace(paths.GlobalAuth) == "" {
		return zero, errors.New("config: global auth path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	unlock, err := lockedfile.Lock(ctx, path)
	if err != nil {
		return zero, fmt.Errorf("config: lock %s credentials: %w", label, err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	previous, err := loadCredentialFile[T](paths, path, label)
	if err != nil {
		return zero, err
	}
	next, err := update(previous)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	switch {
	case next == previous:
		return next, nil
	case next == zero:
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return zero, err
		}
		return zero, nil
	case !valid(next):
		return zero, fmt.Errorf("config: incomplete or invalid %s credentials", label)
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return zero, err
	}
	if err := lockedfile.Write(ctx, path, append(data, '\n')); err != nil {
		return zero, err
	}
	return next, nil
}
