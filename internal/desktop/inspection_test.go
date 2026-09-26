package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyInspectionPreservesMissingAndUnknownGrants(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"granted", "missing", "unknown", "foreign", "changed", "absent", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			const binary, endpoint = "/synthetic/CuaDriver.app/Contents/MacOS/cua-driver", "/synthetic/cua.sock"
			permission := permissionFixture(binary)
			if kind == "missing" {
				permission["accessibility"] = false
			}
			if kind == "unknown" {
				delete(permission, "screen_recording")
			}
			if kind == "foreign" {
				permission["source"].(map[string]any)["pid"] = 999
			}
			// Upstream historical capture fields must not become current evidence.
			permission["screen_recording_capturable"] = true
			calls, probes, connects := 0, 0, 0
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				calls++
				switch name {
				case "get_config":
					return structuredReply(map[string]string{"version": DriverVersion, "platform": "macos"}), nil, true
				case "check_permissions":
					if len(args) != 1 || args["prompt"] != false {
						t.Fatal("inspection could prompt", args)
					}
					return structuredReply(permission), nil, true
				default:
					t.Fatalf("read-only inspection dispatched %s", name)
					return Reply{}, nil, true
				}
			}}
			s := &serviceConnector{binary: binary, endpoint: endpoint,
				connect: func(context.Context) (driverClient, error) { connects++; return f, nil },
				status: func(ctx context.Context) (string, error) {
					probes++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 5*time.Second {
						t.Fatal("inspection has no bounded deadline")
					}
					if kind == "absent" {
						return "", serviceError("not_running", "synthetic absent service")
					}
					if kind == "cancelled" {
						<-ctx.Done()
						return "", ctx.Err()
					}
					status := serviceStatusFixture(endpoint)
					if kind == "changed" && probes == 2 {
						status = strings.Replace(status, "pid: 4321", "pid: 1234", 1)
					}
					return status, nil
				},
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			report, err := s.inspection(ctx)
			accepted := kind == "granted" || kind == "missing"
			if (err == nil) != accepted || report.ConnectionVerified != accepted || report.CheckedAt.IsZero() {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if f.closed != connects || calls > 2 {
				t.Fatal("inspection leaked or repeated calls")
			}
			if !accepted && (report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown) {
				t.Fatal("unverified identity published permissions", report)
			}
			if kind == "missing" && (report.Accessibility != PermissionMissing || report.ScreenRecording != PermissionGranted) {
				t.Fatal("missing grant became unknown or ready", report)
			}
			if kind == "absent" && (connects != 0 || !serviceHasCode(err, "not_running")) {
				t.Fatal("absent service spawned a proxy")
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestCaptureStatusComesFromActualRequestedObservation(t *testing.T) {
	t.Parallel()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2100, 2))); err != nil {
		t.Fatal(err)
	}
	f := &fakeDriver{image: data.Bytes()}
	m := newManager(func(context.Context) (driverClient, error) { return f, nil })
	defer m.Close()
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_ = observed(t, r, false)
	if !m.Status().CaptureCheckedAt.IsZero() {
		t.Fatal("semantic observation became capture evidence")
	}
	_ = observed(t, r, true)
	if !m.Status().CaptureAvailable || m.Status().CaptureCheckedAt.IsZero() {
		t.Fatal("valid screenshot not recorded")
	}
	f.image = []byte("invalid screenshot")
	_ = observed(t, r, true)
	if m.Status().CaptureAvailable {
		t.Fatal("failed capture retained the earlier success state")
	}
}
