package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/lockedfile"
)

func mutableSetting(setting Setting) bool {
	switch setting {
	case SettingProvider, SettingModel, SettingThinking, SettingCustomBaseURL, SettingBrowserHeaded:
		return true
	default:
		return false
	}
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
	validated, err := (Config{}).WithSettings(changes)
	if err != nil {
		return err
	}
	patch := make(map[string]any, len(changes))
	for key, value := range changes {
		if key == SettingBrowserHeaded {
			patch[string(key)] = validated.BrowserHeaded
		} else {
			patch[string(key)] = strings.TrimSpace(value)
		}
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

func patchFile(ctx context.Context, path string, patch map[string]any) error {
	return editFile(ctx, path, func(values map[string]any) error {
		for key, value := range patch {
			values[key] = value
		}
		return nil
	})
}

// editFile is the single locked read/modify/replace primitive for preferences
// and API keys. The callback must perform only bounded in-memory work.
func editFile(ctx context.Context, path string, edit func(map[string]any) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return editLockedFile(ctx, path, edit)
}

var errConfigUnchanged = errors.New("configuration unchanged")

// editLockedFile holds the file's cross-process lock across one atomic
// replacement. Its caller must supply a bounded context: ordinary edits use
// five seconds; MCP OAuth refresh may hold the lock during its bounded token
// exchange. A nil error means the edit was saved or left the file unchanged.
func editLockedFile(ctx context.Context, path string, edit func(map[string]any) error) (returnErr error) {
	unlock, err := lockedfile.Lock(ctx, path)
	if err != nil {
		return fmt.Errorf("config: lock configuration (check permissions): %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	values, err := readValues(ctx, path)
	if err != nil {
		return fmt.Errorf("config: existing file left unchanged: %w", err)
	}
	if err := edit(values); err != nil {
		if errors.Is(err, errConfigUnchanged) {
			return ctx.Err()
		}
		return fmt.Errorf("config: %w", err)
	}
	if err := writeJSON(ctx, path, values); err != nil {
		return fmt.Errorf("config: save %s: %w", path, err)
	}
	return nil
}

// writeJSON replaces a complete document; successful reads see old or new bytes.
// Writers must hold the file lock across their read/modify/write operation.
func writeJSON(ctx context.Context, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return lockedfile.Write(ctx, path, append(data, '\n'))
}
