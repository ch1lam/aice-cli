package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestLoadFilesWithWindowsSharingConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := Paths{
		GlobalSettings: filepath.Join(dir, "settings.json"),
		GlobalAuth:     filepath.Join(dir, "auth.json"),
		GlobalTrust:    filepath.Join(dir, "trust.json"),
		BinDir:         filepath.Join(dir, "bin"),
	}
	if err := os.WriteFile(paths.GlobalAuth, []byte(`{"openai_api_key":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(paths.GlobalAuth)
	if err != nil {
		t.Fatal(err)
	}
	// Deny sharing until the loader exhausts its bounded wait. This makes the
	// production read path fail deterministically without racing a rename.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	if _, err := LoadFiles(paths, LoadOptions{}); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("load error = %v, want deadline and sharing violation", err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	c, err := LoadFiles(paths, LoadOptions{})
	if err != nil || c.OpenAIAPIKey != "fixture" {
		t.Fatalf("load after sharing conflict cleared: %v", err)
	}
}

func TestSaveSettingsWithOpenWindowsReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := Paths{
		GlobalSettings: filepath.Join(dir, "settings.json"),
		GlobalAuth:     filepath.Join(dir, "auth.json"),
		GlobalTrust:    filepath.Join(dir, "trust.json"),
		BinDir:         filepath.Join(dir, "bin"),
	}
	const original = "{\"model\":\"old\"}\n"
	if err := os.WriteFile(paths.GlobalSettings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	// os.Open on Windows does not share delete access. Keep the reader open
	// through the deadline to exercise a real replacement conflict.
	reader, err := os.Open(paths.GlobalSettings)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = SaveSettingsFile(ctx, paths, map[Setting]string{SettingModel: "new"})
	if !errors.Is(err, context.DeadlineExceeded) ||
		(!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
		t.Fatalf("save error = %v, want deadline and Windows replacement conflict", err)
	}
	data, err := os.ReadFile(paths.GlobalSettings)
	if err != nil || string(data) != original {
		t.Fatalf("original file changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("lock or temporary file not cleaned up: %v, %v", entries, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettingsFile(t.Context(), paths, map[Setting]string{SettingModel: "new"}); err != nil {
		t.Fatalf("save after reader closed: %v", err)
	}
}
