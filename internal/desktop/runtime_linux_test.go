package desktop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLinuxRuntimeRejectsExistingRestrictionsWithoutPrivateFallback(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"mode", "policy", "manifest", "unknown", "foreign-peer"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			binary := filepath.Join(directory, "driver")
			endpoint := filepath.Join(directory, "service.sock")
			output := serviceStatusFixture(endpoint)
			want := "external_restriction"
			switch kind {
			case "mode":
				output = strings.Replace(output, "standard", "unrestricted", 1)
			case "policy":
				output = strings.Replace(output, "user policy: configured=false", "user policy: configured=true", 1)
			case "manifest":
				output = strings.Replace(output, "capability manifest: configured=false", "capability manifest: configured=true", 1)
			case "unknown":
				output, want = "unrecognized service diagnostic", "service_unknown"
			case "foreign-peer":
				want = "identity_mismatch"
			}
			// Record command names only. A fallback would run mcp and leave a
			// distinct marker even if the synthetic handshake immediately failed.
			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> '%s/calls'\n[ \"$1\" = status ] || exit 1\ncat <<'STATUS'\n%s\nSTATUS\n", directory, output)
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			c, err := dialLinuxRuntime(t.Context(), binary, endpoint)
			if c != nil || !serviceHasCode(err, want) {
				t.Fatal("incompatible service admission", c, err)
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil || strings.Contains(string(calls), "mcp") {
				t.Fatal("service failure attempted an owned runtime", string(calls), err)
			}
		})
	}
}

func TestLinuxRuntimeOwnedProcessHasFixedAuthorityAndNoDaemon(t *testing.T) {
	t.Setenv("CUA_DRIVER_PERMISSION_MODE", "unrestricted")
	t.Setenv("CUA_DRIVER_HOST_BUNDLE_ID", "synthetic.invalid")
	t.Setenv("OPENAI_API_KEY", "synthetic-secret")
	t.Setenv("LD_PRELOAD", "/synthetic/loader")
	t.Setenv("XDG_DATA_HOME", "/synthetic/applications")
	binary := filepath.Join(t.TempDir(), "driver")
	transport, err := newLinuxOwnedTransport(binary)
	if err != nil {
		t.Fatal(err)
	}
	cmd := transport.command
	if !slices.Equal(cmd.Args, []string{binary, "mcp", "--direct"}) || cmd.Dir != filepath.Dir(binary) || cmd.Stderr != io.Discard {
		t.Fatal("unexpected owned process configuration")
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") || strings.HasPrefix(entry, "LD_PRELOAD=") || strings.HasPrefix(entry, "CUA_DRIVER_HOST_BUNDLE_ID=") || entry == "CUA_DRIVER_PERMISSION_MODE=unrestricted" {
			t.Fatal("owned process inherited forbidden environment")
		}
	}
	if !slices.Contains(cmd.Env, "CUA_DRIVER_PERMISSION_MODE=standard") || !slices.Contains(cmd.Env, "XDG_DATA_HOME=/synthetic/applications") {
		t.Fatal("owned process lost fixed mode or launcher environment")
	}
	if _, err := newLinuxOwnedTransport("relative-driver"); err == nil {
		t.Fatal("relative executable accepted")
	}
}
