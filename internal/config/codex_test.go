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

func TestCodexCredentialsRotateAtomicallyAndPreserveKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := Paths{GlobalAuth: filepath.Join(dir, "auth.json"), GlobalSettings: filepath.Join(dir, "settings.json"), GlobalTrust: filepath.Join(dir, "trust.json"), BinDir: filepath.Join(dir, "bin")}
	if err := SaveOpenAIAPIKeyFile(paths, "api-key"); err != nil {
		t.Fatal(err)
	}
	credential := CodexCredentials{AccessToken: "access", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := UpdateCodexCredentials(t.Context(), paths, func(CodexCredentials) (CodexCredentials, error) { return credential, nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFiles(paths, func(string) (string, bool) { return "", false })
	if err != nil || !loaded.CodexCredentials.Configured() || loaded.OpenAIAPIKey != "api-key" {
		t.Fatalf("load credentials: %v", err)
	}
	info, err := os.Stat(CodexAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	before, err := os.ReadFile(CodexAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("refresh failed")
	if _, err := UpdateCodexCredentials(t.Context(), paths, func(CodexCredentials) (CodexCredentials, error) { return CodexCredentials{}, wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v", err)
	}
	after, err := os.ReadFile(CodexAuthPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed refresh altered stored credentials")
	}
	if err := SaveCustomAPIKeyFile(paths, "other-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCodexCredentials(t.Context(), paths, func(CodexCredentials) (CodexCredentials, error) { return CodexCredentials{}, nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadFiles(paths, func(string) (string, bool) { return "", false })
	if err != nil || loaded.CodexCredentials.Configured() || loaded.OpenAIAPIKey != "api-key" || loaded.CustomAPIKey != "other-key" {
		t.Fatalf("logout did not preserve API credentials: %v", err)
	}
}

func TestCodexCredentialLockRereadsAndCancels(t *testing.T) {
	t.Parallel()
	paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	firstEntered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := UpdateCodexCredentials(t.Context(), paths, func(CodexCredentials) (CodexCredentials, error) {
			close(firstEntered)
			<-release
			return CodexCredentials{AccessToken: "new", RefreshToken: "rotated", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
		})
		if err != nil {
			t.Error(err)
		}
	})
	<-firstEntered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := UpdateCodexCredentials(ctx, paths, func(CodexCredentials) (CodexCredentials, error) {
		t.Error("cancelled writer ran")
		return CodexCredentials{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v", err)
	}
	close(release)
	wg.Wait()
	_, err = UpdateCodexCredentials(t.Context(), paths, func(current CodexCredentials) (CodexCredentials, error) {
		if current.RefreshToken != "rotated" {
			t.Error("writer did not read latest token")
		}
		return current, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
