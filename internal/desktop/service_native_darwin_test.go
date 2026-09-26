//go:build integration && darwin

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in negative test contacts only an absent, isolated socket. It never
// connects to the user's daemon, requests permissions or captures the desktop.
func TestNativeCuaProxyRefusesAutolaunch(t *testing.T) {
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if binary == "" {
		t.Skip("set AICE_CUA_TEST_BINARY to the verified pinned macOS App binary")
	}
	home := t.TempDir()
	endpoint := filepath.Join(home, "absent.sock")
	transport, err := newProcessTransport(binary, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport.command.Env = driverEnvironment([]string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + home})
	// This failure has no desktop content. Inspect only the fixed no-service
	// diagnostic to distinguish refusal from an unrelated crash or timeout.
	var diagnostic serviceOutput
	transport.command.Stderr = &diagnostic
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := connect(ctx, transport)
	if err == nil {
		_ = c.close()
		t.Fatal("absent service was connected or launched")
	}
	if transport.command.ProcessState == nil || transport.command.ProcessState.ExitCode() != 1 || !strings.Contains(diagnostic.String(), "no Cua Driver daemon listening on") {
		t.Fatal("proxy did not report the expected no-autolaunch refusal", err)
	}
	if _, err := os.Lstat(endpoint); !os.IsNotExist(err) {
		t.Fatal("service endpoint appeared", err)
	}
}

func TestNativeCuaStatusEstablishesAbsence(t *testing.T) {
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if binary == "" {
		t.Skip("set AICE_CUA_TEST_BINARY to the verified pinned macOS App binary")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	endpoint := filepath.Join(home, "absent.sock")
	_, err := serviceCommand(t.Context(), binary, "status", "--socket", endpoint)
	if !serviceHasCode(err, "not_running") {
		t.Fatalf("fixed public status did not establish absence: %v", err)
	}
	if _, err := os.Lstat(endpoint); !os.IsNotExist(err) {
		t.Fatal("read-only status created service endpoint", err)
	}
}
