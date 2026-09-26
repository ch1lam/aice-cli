package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serviceStatusFixture(endpoint string) string {
	return "Cua Driver daemon is running\n  socket: " + endpoint + "\n" +
		"  pid: 4321\n  permission mode: standard (default)\n" +
		"  user policy: configured=false, active=false, valid=true\n" +
		"  managed policy: configured=false, active=false, valid=true\n" +
		"  authorization host: standalone (verified)\n" +
		"  capability manifest: configured=false, approved_at_startup=false, valid=true\n"
}

func permissionFixture(binary string) map[string]any {
	return map[string]any{"accessibility": true, "screen_recording": true, "source": map[string]any{
		"attribution": "driver-daemon", "pid": 4321, "executable": binary, "bundle_id": "com.trycua.driver",
	}}
}

func TestServiceAdmissionDoesNotObserveOrReconfigure(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "CuaDriver.app", "Contents", "MacOS", "cua-driver")
	endpoint := filepath.Join(t.TempDir(), "cua.sock")
	for _, kind := range []string{"ready", "version", "platform", "missing-grant", "unknown-grant", "identity", "pid", "executable", "bundle", "changed", "transport", "domain", "mode", "policy", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			probes, connects := 0, 0
			permission := permissionFixture(binary)
			source := permission["source"].(map[string]any)
			switch kind {
			case "missing-grant":
				permission["accessibility"] = false
			case "unknown-grant":
				delete(permission, "screen_recording")
			case "identity":
				source["attribution"] = "host"
			case "pid":
				source["pid"] = 1234
			case "executable":
				source["executable"] = filepath.Join(t.TempDir(), "cua-driver")
			case "bundle":
				source["bundle_id"] = "unverified.bundle"
			}
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name == "get_config" {
					version, platform := DriverVersion, "macos"
					if kind == "version" {
						version = "0.0.0"
					}
					if kind == "platform" {
						platform = "linux"
					}
					return structuredReply(map[string]string{"version": version, "platform": platform}), nil, true
				}
				if name == "check_permissions" {
					if len(args) != 1 || args["prompt"] != false {
						t.Fatal("permission check could prompt", args)
					}
					if kind == "transport" {
						return Reply{}, io.EOF, true
					}
					reply := structuredReply(permission)
					reply.IsError = kind == "domain"
					return reply, nil, true
				}
				t.Fatalf("admission attempted non-inspection tool %s", name)
				return Reply{}, nil, false
			}}
			s := &serviceConnector{binary: binary, endpoint: endpoint, connect: func(context.Context) (driverClient, error) {
				connects++
				return f, nil
			}, status: func(context.Context) (string, error) {
				probes++
				value := serviceStatusFixture(endpoint)
				switch kind {
				case "changed":
					if probes == 2 {
						value = strings.Replace(value, "pid: 4321", "pid: 1234", 1)
					}
				case "mode":
					value = strings.Replace(value, "standard", "unrestricted", 1)
				case "policy":
					value = strings.Replace(value, "configured=false", "configured=true", 1)
				case "manifest":
					value = strings.Replace(value, "manifest: configured=false", "manifest: configured=true", 1)
				}
				return value, nil
			}}
			c, err := s.dial(t.Context())
			if kind == "ready" {
				if err != nil || c == nil || probes != 2 || connects != 1 || f.closed != 0 || f.count("check_permissions") != 1 {
					t.Fatal("unexpected admission", err, probes, connects, f.closed)
				}
				_ = c.close()
				return
			}
			if err == nil || c != nil {
				t.Fatal("unsafe service accepted", kind)
			}
			if kind == "mode" || kind == "policy" || kind == "manifest" {
				var failure *ServiceError
				if connects != 0 || !errors.As(err, &failure) || failure.Code != "external_restriction" {
					t.Fatal("external restriction misclassified", connects, err)
				}
			} else if connects != 1 || f.closed != 1 {
				t.Fatal("rejected connection leaked or retried", connects, f.closed)
			}
		})
	}
}

func TestServiceStatusMissingAmbiguousOrForeignIdentity(t *testing.T) {
	t.Parallel()
	good := serviceStatusFixture("/synthetic/cua.sock")
	for _, value := range []string{
		"", "Cua Driver daemon is not running",
		strings.Replace(good, "pid: 4321", "pid: unknown (no pid file)", 1),
		strings.Replace(good, "/synthetic/cua.sock", "/foreign/cua.sock", 1),
		strings.Replace(good, "  permission mode: standard (default)\n", "", 1),
		strings.Replace(good, "  user policy: configured=false, active=false, valid=true\n", "", 1),
		strings.Replace(good, "  capability manifest: configured=false, approved_at_startup=false, valid=true\n", "", 1),
		good + "  permission mode: unrestricted (environment)\n",
	} {
		if _, err := parseServiceStatus(value, "/synthetic/cua.sock"); err == nil {
			t.Fatal("ambiguous state accepted", value)
		}
	}
}

func TestServiceInspectionSchemaRequiresExplicitReadonlyPrompt(t *testing.T) {
	t.Parallel()
	schemas := map[string]json.RawMessage{
		"get_config":        json.RawMessage(`{"type":"object","properties":{}}`),
		"check_permissions": json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"boolean"}}}`),
	}
	if err := validateInspectionSchemas(schemas); err != nil {
		t.Fatal(err)
	}
	schemas["check_permissions"] = json.RawMessage(`{"type":"object","properties":{}}`)
	if err := validateInspectionSchemas(schemas); err == nil {
		t.Fatal("missing read-only switch accepted")
	}
}

func TestServiceProxyCannotAutolaunch(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "cua-driver")
	endpoint := filepath.Join(t.TempDir(), "cua.sock")
	transport, err := newProcessTransport(binary, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{binary, "mcp", "--socket", endpoint, "--embedded"}
	if strings.Join(transport.command.Args, "\n") != strings.Join(want, "\n") {
		t.Fatal("proxy could launch a service", transport.command.Args)
	}
	for _, entry := range transport.command.Env {
		if strings.HasPrefix(entry, "CUA_DRIVER_HOST_BUNDLE_ID=") {
			t.Fatal("proxy claimed a host identity")
		}
	}
}

func TestServiceCommandOutputBoundAndCancellation(t *testing.T) {
	// The production environment excludes custom sentinels. Use a test selection
	// argument to enter the dedicated synthetic helper below instead.
	t.Parallel()
	if _, err := serviceCommand(t.Context(), os.Args[0], "-test.run=^TestServiceStatusOutputHelper$", "overflow"); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatal("oversized management output not rejected by bound", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := serviceCommand(ctx, os.Args[0], "-test.run=^TestServiceStatusOutputHelper$", "block"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancellation lost", err)
	}
}

func TestServiceStatusOutputHelper(t *testing.T) {
	last := os.Args[len(os.Args)-1]
	if last != "overflow" && last != "block" {
		return
	}
	if last == "overflow" {
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 40*1024))
		os.Exit(0)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
