//go:build integration && windows

package desktop

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
)

// Status only: never starts/installs a service, captures or requests elevation.
func TestNativeWindowsServiceInspection(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE_STATUS") != "1" {
		t.Skip("requires explicit opt-in and an already installed pinned Windows service")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	installed, err := deps.InstallCua(ctx, deps.DefaultOptions().WithNoInstall(true))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		report, err := Inspect(ctx, installed.Installation.Binary, `\\.\pipe\cua-driver`)
		if err != nil || !report.ConnectionVerified || report.Windows == nil || report.Windows.SessionID == nil || report.CheckedAt.IsZero() {
			t.Fatal("native Windows status admission failed", err)
		}
		if report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown {
			t.Fatal("Windows inspection invented macOS grants")
		}
	}
	t.Log("two read-only inspections passed; shared service preserved; no native input/capture acceptance claimed")
}
