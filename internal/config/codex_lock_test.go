package config

import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func TestCodexCredentialLockRetry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name            string
		retryPermission bool
		failures        []error
		cancelOnAttempt bool
		wantErr         error
		wantCause       error
		wantAttempts    int
	}{
		{name: "immediate acquisition", wantAttempts: 1},
		{name: "existing lock then acquisition", failures: []error{os.ErrExist}, wantAttempts: 2},
		{name: "Windows access denied then acquisition", retryPermission: true,
			failures: []error{os.ErrPermission, os.ErrExist}, wantAttempts: 3},
		{name: "Unix permission denied", failures: []error{os.ErrPermission},
			wantErr: os.ErrPermission, wantAttempts: 1},
		{name: "other filesystem error", retryPermission: true, failures: []error{os.ErrNotExist},
			wantErr: os.ErrNotExist, wantAttempts: 1},
		{name: "cancel during Windows contention", retryPermission: true,
			failures: []error{os.ErrPermission}, cancelOnAttempt: true,
			wantErr: context.Canceled, wantCause: os.ErrPermission, wantAttempts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				attempts := 0
				err := acquireCodexCredentialLock(ctx, "auth.lock", func(path string, mode os.FileMode) error {
					if path != "auth.lock" || mode != 0o700 {
						t.Fatalf("mkdir arguments = %q, %v", path, mode)
					}
					attempts++
					if tt.cancelOnAttempt {
						cancel()
					}
					if attempts <= len(tt.failures) {
						return &os.PathError{Op: "mkdir", Path: path, Err: tt.failures[attempts-1]}
					}
					return nil
				}, tt.retryPermission)
				if !errors.Is(err, tt.wantErr) || (tt.wantCause != nil && !errors.Is(err, tt.wantCause)) {
					t.Fatalf("lock error = %v, want %v with cause %v", err, tt.wantErr, tt.wantCause)
				}
				if attempts != tt.wantAttempts {
					t.Fatalf("attempts = %d, want %d", attempts, tt.wantAttempts)
				}
			})
		})
	}
}

func TestCodexCredentialLockPersistentAccessDenied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		attempts := 0
		start := time.Now()
		err := acquireCodexCredentialLock(ctx, "auth.lock", func(path string, _ os.FileMode) error {
			attempts++
			return &os.PathError{Op: "mkdir", Path: path, Err: os.ErrPermission}
		}, true)
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("lock wait = %v, want deadline and access denied", err)
		}
		if attempts < 2 || time.Since(start) != time.Minute {
			t.Fatalf("attempts = %d, elapsed = %v; want retries until the deadline", attempts, time.Since(start))
		}
	})
}
