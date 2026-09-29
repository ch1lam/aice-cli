package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexCredentialCleanupCommit(t *testing.T) {
	old := CodexCredentials{AccessToken: "old", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	next := old
	next.AccessToken = "next"
	testOAuthCredentialCleanup(t, old, next, CodexAuthPath, LoadCodexCredentials, UpdateCodexCredentials)
}

func TestClaudeSubscriptionCredentialCleanupCommit(t *testing.T) {
	old := ClaudeSubscriptionCredentials{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	next := old
	next.AccessToken = "next"
	testOAuthCredentialCleanup(t, old, next, ClaudeSubscriptionAuthPath, LoadClaudeSubscriptionCredentials, UpdateClaudeSubscriptionCredentials)
}

func testOAuthCredentialCleanup[T comparable](t *testing.T, old, next T,
	authPath func(Paths) string,
	load func(Paths) (T, error),
	update func(context.Context, Paths, func(T) (T, error)) (T, error),
) {
	t.Helper()
	var zero T
	updateErr := errors.New("update rejected")
	for _, tc := range []struct {
		name          string
		value         T
		updateErr     error
		removeInHook  bool
		wantCommitted bool
		wantDisk      T
	}{
		{name: "replacement", value: next, wantCommitted: true, wantDisk: next},
		{name: "deletion", value: zero, wantCommitted: true, wantDisk: zero},
		{name: "unchanged", value: old, wantDisk: old},
		{name: "callback failure", updateErr: updateErr, wantDisk: old},
		{name: "already removed", value: zero, removeInHook: true, wantDisk: zero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
			if _, err := update(t.Context(), paths, func(T) (T, error) { return old, nil }); err != nil {
				t.Fatal(err)
			}
			lock := authPath(paths) + ".lock"
			_, err := update(t.Context(), paths, func(previous T) (T, error) {
				if previous != old {
					t.Fatal("callback did not receive saved credential")
				}
				// Make removal of this writer's own lock directory fail, after
				// the callback returns and the real write/delete has run.
				if err := os.WriteFile(filepath.Join(lock, "cleanup-blocker"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if tc.removeInHook {
					if err := os.Remove(authPath(paths)); err != nil {
						t.Fatal(err)
					}
				}
				return tc.value, tc.updateErr
			})
			loaded, loadErr := load(paths)
			if loadErr != nil || loaded != tc.wantDisk {
				t.Fatalf("unexpected disk credential after update: %v", loadErr)
			}
			if err == nil || WasCommitted(err) != tc.wantCommitted {
				t.Errorf("cleanup error = %v, committed = %v; want %v", err, WasCommitted(err), tc.wantCommitted)
			}
			if tc.updateErr != nil && !errors.Is(err, tc.updateErr) {
				t.Errorf("update error cause lost: %v", err)
			}
			var cleanupErr *os.PathError
			if !errors.As(err, &cleanupErr) || cleanupErr.Path != lock || !errors.Is(err, cleanupErr.Err) {
				t.Errorf("lock cleanup cause lost: %v", err)
			}
		})
	}
}
