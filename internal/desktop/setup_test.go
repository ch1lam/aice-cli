package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupFixture(t *testing.T) (*nativeService, *int, *int) {
	t.Helper()
	binary, endpoint := filepath.Join(t.TempDir(), "cua-driver"), filepath.Join(t.TempDir(), "cua.sock")
	launches, grants := 0, 0
	ready := false
	fake := func() *fakeDriver {
		return &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
			switch name {
			case "get_config":
				return structuredReply(map[string]string{"version": DriverVersion, "platform": "macos"}), nil, true
			case "check_permissions":
				if args["prompt"] != false {
					t.Fatal("admission requested authorization")
				}
				permission := permissionFixture(binary)
				permission["accessibility"], permission["screen_recording"] = ready, ready
				return structuredReply(permission), nil, true
			default:
				t.Fatalf("setup tried desktop tool %s", name)
				return Reply{}, nil, false
			}
		}}
	}
	n := &nativeService{
		connector: &serviceConnector{binary: binary, endpoint: endpoint, status: func(context.Context) (string, error) { return serviceStatusFixture(endpoint), nil }, connect: func(context.Context) (driverClient, error) { return fake(), nil }},
		lock:      func(context.Context) (func() error, error) { return func() error { return nil }, nil },
		launch:    func(context.Context) error { launches++; return nil },
		grant:     func(context.Context) error { grants++; ready = true; return nil },
	}
	return n, &launches, &grants
}

func TestNativeSetupPreservesPartialExternalFacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"ready", "grant-fails", "lost-after-grant", "wrong-identity", "restricted", "cancel-before-grant"} {
		t.Run(kind, func(t *testing.T) {
			n, launches, grants := setupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "grant-fails":
				n.grant = func(context.Context) error { (*grants)++; return io.EOF }
			case "lost-after-grant":
				grant := n.grant
				n.grant = func(ctx context.Context) error {
					_ = grant(ctx)
					n.connector.status = func(context.Context) (string, error) { return "", io.EOF }
					return nil
				}
			case "wrong-identity":
				n.connector.binary = filepath.Join(t.TempDir(), "wrong")
			case "restricted":
				n.connector.status = func(context.Context) (string, error) {
					return strings.Replace(serviceStatusFixture(n.connector.endpoint), "standard", "unrestricted", 1), nil
				}
			case "cancel-before-grant":
				connect := n.connector.connect
				n.connector.connect = func(ctx context.Context) (driverClient, error) { c, err := connect(ctx); cancel(); return c, err }
			}
			result, err := n.setup(ctx)
			if *launches != 0 || result.LaunchRequested {
				t.Fatal("setup relaunched existing service")
			}
			if kind == "ready" {
				if err != nil || !result.Ready || !result.AuthorizationCompleted || *grants != 1 {
					t.Fatalf("ready result=%+v err=%v", result, err)
				}
				return
			}
			if err == nil || result.Ready {
				t.Fatalf("failure claimed ready: %+v %v", result, err)
			}
			requested := kind == "grant-fails" || kind == "lost-after-grant"
			if result.AuthorizationRequested != requested || result.AuthorizationCompleted != (kind == "lost-after-grant") {
				t.Fatalf("partial facts lost: %+v", result)
			}
			if !requested && *grants != 0 {
				t.Fatal("grant dispatched before admission/cancellation checks")
			}
		})
	}
}

func TestNativeLazyStartOnlyOnEstablishedAbsence(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "present", "unknown", "policy", "another-started", "launch-lost"} {
		t.Run(kind, func(t *testing.T) {
			n, launches, grants := setupFixture(t)
			probes, locks := 0, 0
			n.lock = func(context.Context) (func() error, error) { locks++; return func() error { return nil }, nil }
			n.connector.status = func(context.Context) (string, error) {
				probes++
				switch kind {
				case "unknown":
					return "", io.EOF
				case "policy":
					return strings.Replace(serviceStatusFixture(n.connector.endpoint), "configured=false", "configured=true", 1), nil
				case "missing", "launch-lost":
					if *launches == 0 {
						return "", serviceError("not_running", "absent")
					}
				case "another-started":
					if probes == 1 {
						return "", serviceError("not_running", "absent")
					}
				}
				return serviceStatusFixture(n.connector.endpoint), nil
			}
			if kind == "launch-lost" {
				n.launch = func(context.Context) error { (*launches)++; return io.EOF }
			}
			requested, err := n.ensure(t.Context())
			wantLaunch := kind == "missing" || kind == "launch-lost"
			if requested != wantLaunch || (*launches == 1) != wantLaunch || *grants != 0 {
				t.Fatalf("requested=%v launches=%d grants=%d", requested, *launches, *grants)
			}
			wantErr := kind == "unknown" || kind == "policy" || kind == "launch-lost"
			if (err != nil) != wantErr {
				t.Fatalf("err=%v", err)
			}
			if (kind == "present" || kind == "unknown" || kind == "policy") && locks != 0 {
				t.Fatal("read-only admission acquired startup lock")
			}
		})
	}
}

func TestNativeStartupLockCancellationDoesNotSteal(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	unlock, err := lockNativeSetup(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := lockNativeSetup(ctx, directory); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock=%v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".aice-setup.lock")); err != nil {
		t.Fatal("holder lock removed", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	next, err := lockNativeSetup(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := next(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLaunchUsesSignedAppStandardAndNoUnsolicitedGrant(t *testing.T) {
	t.Parallel()
	endpoint := "/synthetic/cua.sock"
	want := []string{"-n", "-g", "-a", "/Applications/CuaDriver.app", "--env", "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "--env", "CUA_DRIVER_RS_UPDATE_CHECK=false", "--env", "CUA_DRIVER_PERMISSION_MODE=standard", "--env", "CUA_DRIVER_EMBEDDED=0", "--args", "serve", "--permission-mode", "standard", "--no-permissions-gate", "--socket", endpoint}
	if got := macLaunchArguments(endpoint); !reflect.DeepEqual(got, want) {
		t.Fatalf("launch=%v", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runNativeManagement(ctx, time.Second, filepath.Join(t.TempDir(), "must-not-exist")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}
