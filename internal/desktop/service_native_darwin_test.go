//go:build integration && darwin

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dump-docs inspects the canonical inventory without constructing a runtime.
// This test never connects to a daemon or requests desktop access.
func TestNativeCuaSchemaInventory(t *testing.T) {
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if binary == "" {
		t.Skip("set AICE_CUA_TEST_BINARY to the verified pinned macOS App binary")
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "dump-docs", "--type", "mcp")
	command.Env = driverEnvironment([]string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + home})
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		t.Fatal("metadata export failed", err)
	}
	var inventory struct {
		Version string `json:"version"`
		Tools   []struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(output.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Version != DriverVersion {
		t.Fatal("metadata binary version mismatch")
	}
	schemas := make(map[string]json.RawMessage, len(inventory.Tools))
	for _, tool := range inventory.Tools {
		if _, exists := schemas[tool.Name]; exists {
			t.Fatal("duplicate exported tool", tool.Name)
		}
		schemas[tool.Name] = tool.Schema
	}
	if _, err := reviewedMacTools(schemas); err != nil {
		t.Fatal(err)
	}
}

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
	report, err := Inspect(t.Context(), binary, endpoint)
	if !serviceHasCode(err, "not_running") || report.ConnectionVerified || report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown {
		t.Fatalf("absent service became permission/capture readiness: %+v %v", report, err)
	}
	if _, err := os.Lstat(endpoint); !os.IsNotExist(err) {
		t.Fatal("inspection started a service", err)
	}
}
