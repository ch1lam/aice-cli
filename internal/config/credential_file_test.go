package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/lockedfile"
)

func TestCodexCredentialUpdate(t *testing.T) {
	old := CodexCredentials{AccessToken: "old", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	next := old
	next.AccessToken = "next"
	testOAuthCredentialUpdate(t, old, next, CodexAuthPath, LoadCodexCredentials, UpdateCodexCredentials)
}

func TestClaudeSubscriptionCredentialUpdate(t *testing.T) {
	old := ClaudeSubscriptionCredentials{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	next := old
	next.AccessToken = "next"
	testOAuthCredentialUpdate(t, old, next, ClaudeSubscriptionAuthPath, LoadClaudeSubscriptionCredentials, UpdateClaudeSubscriptionCredentials)
}

func testOAuthCredentialUpdate[T comparable](t *testing.T, old, next T,
	authPath func(Paths) string,
	load func(Paths) (T, error),
	update func(context.Context, Paths, func(T) (T, error)) (T, error),
) {
	t.Helper()
	var zero T
	updateErr := errors.New("update rejected")
	for _, tc := range []struct {
		name         string
		value        T
		updateErr    error
		removeInHook bool
		wantDisk     T
	}{
		{name: "replacement", value: next, wantDisk: next},
		{name: "deletion", value: zero, wantDisk: zero},
		{name: "unchanged", value: old, wantDisk: old},
		{name: "callback failure", updateErr: updateErr, wantDisk: old},
		{name: "already removed", value: zero, removeInHook: true, wantDisk: zero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
			if _, err := update(t.Context(), paths, func(T) (T, error) { return old, nil }); err != nil {
				t.Fatal(err)
			}
			_, err := update(t.Context(), paths, func(previous T) (T, error) {
				if previous != old {
					t.Fatal("callback did not receive saved credential")
				}
				if tc.removeInHook {
					if err := os.Remove(authPath(paths)); err != nil {
						t.Fatal(err)
					}
				}
				return tc.value, tc.updateErr
			})
			if !errors.Is(err, tc.updateErr) {
				t.Fatalf("update error = %v, want %v", err, tc.updateErr)
			}
			loaded, loadErr := load(paths)
			if loadErr != nil || loaded != tc.wantDisk {
				t.Fatalf("unexpected disk credential after update: %v", loadErr)
			}
		})
	}
}

func TestCredentialUpdateWaitsForLockOwner(t *testing.T) {
	t.Parallel()
	paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	for name, update := range map[string]func(context.Context) error{
		"codex": func(ctx context.Context) error {
			_, err := UpdateCodexCredentials(ctx, paths, func(CodexCredentials) (CodexCredentials, error) {
				t.Error("update ran while another writer owned the lock")
				return CodexCredentials{}, nil
			})
			return err
		},
		"claude subscription": func(ctx context.Context) error {
			_, err := UpdateClaudeSubscriptionCredentials(ctx, paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
				t.Error("update ran while another writer owned the lock")
				return ClaudeSubscriptionCredentials{}, nil
			})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := CodexAuthPath(paths)
			if name != "codex" {
				path = ClaudeSubscriptionAuthPath(paths)
			}
			unlock, err := lockedfile.Lock(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			defer cancel()
			if err := update(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lock wait = %v, want deadline", err)
			}
		})
	}
}
