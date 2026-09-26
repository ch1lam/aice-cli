package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLinuxInspectionIdentityAndCapabilities(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"headless", "x11", "wayland", "absent", "restricted", "foreign-peer", "changed-peer", "changed-pid", "wrong-version", "missing-fact", "domain", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			calls, peers, probes := 0, 0, 0
			facts := map[string]any{"x11": false, "atspi": false, "wayland": false, "wayland_enabled": false, "xsend_event": false, "dbus_session_bus_address": "private-address-must-not-be-projected"}
			if kind == "x11" {
				facts["x11"], facts["atspi"], facts["xsend_event"] = true, true, true
			}
			if kind == "wayland" {
				facts["wayland"], facts["wayland_enabled"] = true, true
			}
			if kind == "missing-fact" {
				delete(facts, "atspi")
			}
			f := &fakeDriver{handle: func(ctx context.Context, name string, args map[string]any) (Reply, error, bool) {
				calls++
				if len(args) != 0 {
					t.Fatal("Linux inspection passed unsupported arguments", args)
				}
				switch name {
				case "get_config":
					version := DriverVersion
					if kind == "wrong-version" {
						version = "0.0.0"
					}
					return structuredReply(map[string]string{"version": version, "platform": "linux"}), nil, true
				case "check_permissions":
					if kind == "cancel" {
						return Reply{}, context.Canceled, true
					}
					r := structuredReply(facts)
					r.IsError = kind == "domain"
					return r, nil, true
				default:
					t.Fatalf("inspection dispatched %s", name)
					return Reply{}, nil, true
				}
			}}
			s := linuxInspector{
				service: &serviceConnector{endpoint: "/synthetic/cua.sock", status: func(context.Context) (string, error) {
					probes++
					if kind == "absent" {
						return "", serviceError("not_running", "absent")
					}
					out := serviceStatusFixture("/synthetic/cua.sock")
					if kind == "restricted" {
						out = strings.Replace(out, "standard", "unrestricted", 1)
					}
					if kind == "changed-pid" && probes == 2 {
						out = strings.Replace(out, "4321", "1234", 1)
					}
					return out, nil
				}},
				peer: func(context.Context, int) error {
					peers++
					if kind == "foreign-peer" || kind == "changed-peer" && peers == 2 {
						return serviceError("identity_mismatch", "foreign")
					}
					return nil
				},
				connect: func(context.Context) (driverClient, error) { return f, nil },
			}
			report, err := s.inspect(t.Context())
			accepted := kind == "headless" || kind == "x11" || kind == "wayland"
			if (err == nil) != accepted || report.ConnectionVerified != accepted || report.CheckedAt.IsZero() {
				t.Fatal(report, err)
			}
			if report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown {
				t.Fatal("Linux invented macOS grants", report)
			}
			if !accepted && report.Linux.X11 != PermissionUnknown {
				t.Fatal("unverified capabilities published", report)
			}
			if kind == "headless" && (report.Linux.X11 != PermissionMissing || report.Linux.ATSPI != PermissionMissing) {
				t.Fatal(report)
			}
			if kind == "x11" && report.Linux.X11 != PermissionGranted {
				t.Fatal(report)
			}
			if kind == "wayland" && (report.Linux.WaylandEnvironment != PermissionGranted || report.Linux.X11 != PermissionMissing) {
				t.Fatal(report)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if calls > 0 && f.closed != 1 {
				t.Fatal("inspection leaked client")
			}
			if calls > 2 {
				t.Fatal("inspection repeated calls")
			}
			if (kind == "foreign-peer" || kind == "restricted" || kind == "absent") && calls != 0 {
				t.Fatal("unverified service contacted")
			}
		})
	}
}

func TestLinuxInspectionClientAdmitsOnlyReviewedStatusTools(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"linux-status", "linux-status-drift"} {
		transport, peer := fakeTransport(t, mode)
		c, err := connectReviewed(t.Context(), transport, reviewedLinuxStatusTools)
		if mode == "linux-status-drift" {
			if !serviceHasCode(err, "incompatible_service") || peer.calls.Load() != 0 {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		defer c.close()
		if len(c.tools) != 2 {
			t.Fatal("unreviewed tools admitted", len(c.tools))
		}
		if _, err := c.call(t.Context(), "click", nil); err == nil || peer.calls.Load() != 0 {
			t.Fatal("status client dispatched input")
		}
	}
}

func linuxStatusFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var inventory schemaInventory
	if err := json.Unmarshal(linuxStatusSchemaInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory.Tools
}
