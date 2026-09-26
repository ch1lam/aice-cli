//go:build integration && linux

package desktop

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run only in an isolated container with no user display/bus mounts. The exact
// foreground child is owned by the test; no existing service is stopped.
func TestNativeLinuxHeadlessInspection(t *testing.T) {
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if binary == "" || os.Getenv("AICE_CUA_HEADLESS_CONTAINER") != "1" {
		t.Skip("requires an explicitly supplied verified binary in an isolated headless container")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("headless inspection fixture requires its isolated Docker container")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("binary must be absolute")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	endpoint := filepath.Join(home, "cua.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve", "--socket", endpoint, "--permission-mode", "standard", "--no-permissions-gate")
	cmd.Env = driverEnvironment([]string{"HOME=" + home, "PATH=/usr/bin:/bin", "TMPDIR=" + home, "XDG_RUNTIME_DIR=" + home})
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for {
		if _, err := os.Stat(endpoint); err == nil {
			break
		}
		if err := waitNativeProbe(ctx); err != nil {
			t.Fatal("isolated daemon did not start", err)
		}
	}
	report, err := Inspect(ctx, binary, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ConnectionVerified || report.Linux == nil || report.Linux.X11 != PermissionMissing || report.Linux.ATSPI != PermissionMissing || report.Linux.WaylandEnvironment != PermissionMissing || report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown {
		t.Fatalf("headless state misreported: %+v %+v", report, report.Linux)
	}
	// A second inspection proves closing the inspection proxy left the shared
	// daemon alive, without creating a desktop session or observing a window.
	if _, err := Inspect(ctx, binary, endpoint); err != nil {
		t.Fatal("inspection stopped its service", err)
	}
	t.Log("verified pinned Linux service identity; no X11, AT-SPI or Wayland environment; no capture or input dispatched")
}
