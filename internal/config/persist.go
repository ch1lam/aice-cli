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
	"syscall"
	"time"
)

func mutableSetting(setting Setting) bool {
	switch setting {
	case SettingProvider, SettingModel, SettingThinking, SettingCustomBaseURL:
		return true
	default:
		return false
	}
}

// SaveSettingFile patches a single preference without serializing resolved inputs.
func SaveSettingFile(paths Paths, setting Setting, value string) error {
	return SaveSettingsFile(context.Background(), paths, map[Setting]string{setting: value})
}

// SaveSettingsFile persists one user action under a cross-process lock. Values
// already on disk are preserved, including settings shadowed by higher layers.
func SaveSettingsFile(ctx context.Context, paths Paths, changes map[Setting]string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	// Validate only the new fields. An unrelated, overridden bad field must not
	// prevent saving a valid selection or be copied from the effective snapshot.
	if _, err := (Config{}).WithSettings(changes); err != nil {
		return err
	}
	patch := make(map[string]any, len(changes))
	for key, value := range changes {
		patch[string(key)] = strings.TrimSpace(value)
	}
	return patchFile(ctx, paths.GlobalSettings, patch)
}

func saveAPIKeyFile(paths Paths, providerName, key, apiKey string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return fmt.Errorf("config: %s API key must be one line", providerName)
	}
	return patchFile(context.Background(), paths.GlobalAuth, map[string]any{key: apiKey})
}

func patchFile(ctx context.Context, path string, patch map[string]any) (returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create directory: %w", err)
	}
	lock := path + ".lock"
	if err := acquireConfigLock(ctx, lock, os.Mkdir, runtime.GOOS == "windows"); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, os.Remove(lock)) }()
	values, err := readValues(path)
	if err != nil {
		return fmt.Errorf("config: existing file left unchanged: %w", err)
	}
	for key, value := range patch {
		values[key] = value
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeJSON(ctx, path, values); err != nil {
		return fmt.Errorf("config: save %s: %w", path, err)
	}
	return nil
}

// writeJSON replaces a complete document; readers see either old or new bytes.
// Writers must hold the relevant lock across their read/modify/write operation.
func writeJSON(ctx context.Context, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".aice-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return renameConfigFile(ctx, file.Name(), path, os.Rename, runtime.GOOS == "windows")
}

// renameConfigFile retries Windows sharing conflicts while the caller retains
// the write lock and temporary file. The caller must supply a bounded context.
// Never remove the destination first: readers must still see a complete file.
func renameConfigFile(ctx context.Context, source, target string,
	rename func(string, string) error, windows bool,
) error {
	// Win32 error codes are defined here so retry behavior can be exercised on
	// every test host, without importing a Windows-only package.
	const (
		accessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED
		sharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
	)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		err := rename(source, target)
		if err == nil {
			return nil
		}
		if !windows || (!errors.Is(err, accessDenied) && !errors.Is(err, sharingViolation)) {
			return err
		}
		lastErr = err
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
