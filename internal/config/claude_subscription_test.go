package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestClaudeSubscriptionCredentialsRotateAtomicallyAndPreserveKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := Paths{GlobalAuth: filepath.Join(dir, "auth.json"), GlobalSettings: filepath.Join(dir, "settings.json"), GlobalTrust: filepath.Join(dir, "trust.json"), BinDir: filepath.Join(dir, "bin")}
	if err := SaveOpenAIAPIKeyFile(paths, "api-key"); err != nil {
		t.Fatal(err)
	}
	credential := ClaudeSubscriptionCredentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) { return credential, nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadClaudeSubscriptionCredentials(paths)
	if err != nil || !loaded.Configured() {
		t.Fatalf("load credentials: %v", err)
	}
	info, err := os.Stat(ClaudeSubscriptionAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	before, err := os.ReadFile(ClaudeSubscriptionAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("refresh failed")
	if _, err := UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		return ClaudeSubscriptionCredentials{}, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v", err)
	}
	after, err := os.ReadFile(ClaudeSubscriptionAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed refresh altered stored credentials")
	}
	if err := SaveCustomAPIKeyFile(paths, "other-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		return ClaudeSubscriptionCredentials{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadClaudeSubscriptionCredentials(paths)
	apiConfig, configErr := LoadFiles(paths, LoadOptions{})
	if configErr != nil || apiConfig.OpenAIAPIKey != "api-key" || apiConfig.CustomAPIKey != "other-key" {
		t.Fatalf("lost API credentials: %v", configErr)
	}
	if err != nil || loaded.Configured() {
		t.Fatalf("logout did not preserve API credentials: %v", err)
	}
}

func TestClaudeSubscriptionCredentialLockRereadsAndCancels(t *testing.T) {
	t.Parallel()
	paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	firstEntered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
			close(firstEntered)
			<-release
			return ClaudeSubscriptionCredentials{AccessToken: "new", RefreshToken: "rotated", ExpiresAt: time.Now().Add(time.Hour)}, nil
		})
		if err != nil {
			t.Error(err)
		}
	})
	<-firstEntered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := UpdateClaudeSubscriptionCredentials(ctx, paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		t.Error("cancelled writer ran")
		return ClaudeSubscriptionCredentials{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v", err)
	}
	close(release)
	wg.Wait()
	_, err = UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(current ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		if current.RefreshToken != "rotated" {
			t.Error("writer did not read latest token")
		}
		return current, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSubscriptionCredentialLockWaitPreservesOwnerAndCause(t *testing.T) {
	t.Parallel()
	paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	lock := ClaudeSubscriptionAuthPath(paths) + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	_, err := UpdateClaudeSubscriptionCredentials(ctx, paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		t.Error("update ran while another writer owned the lock")
		return ClaudeSubscriptionCredentials{}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, os.ErrExist) {
		t.Fatalf("lock wait = %v, want deadline and existing lock", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("waiting writer altered the owner's lock: %v", err)
	}
}

func TestClaudeSubscriptionCredentialSurvivesSettingsChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := Paths{GlobalAuth: filepath.Join(dir, "auth.json"), GlobalSettings: filepath.Join(dir, "settings.json"), GlobalTrust: filepath.Join(dir, "trust.json"), BinDir: filepath.Join(dir, "bin")}
	credential := ClaudeSubscriptionCredentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) { return credential, nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFiles(paths, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := loaded.WithSettings(map[Setting]string{SettingProvider: "anthropic-subscription", SettingModel: "claude-sonnet-5"})
	if err != nil || next.ClaudeSubscriptionCredentials != loaded.ClaudeSubscriptionCredentials || !next.ClaudeSubscriptionCredentials.Configured() {
		t.Fatalf("settings lost OAuth credential: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err = UpdateClaudeSubscriptionCredentials(ctx, paths, func(ClaudeSubscriptionCredentials) (ClaudeSubscriptionCredentials, error) {
		cancel()
		return ClaudeSubscriptionCredentials{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write = %v", err)
	}
	after, err := LoadClaudeSubscriptionCredentials(paths)
	if err != nil || after != loaded.ClaudeSubscriptionCredentials {
		t.Fatal("canceled callback changed credential")
	}
}
