package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLinuxCaptureContractDoesNotWeakenMacOrFailedFrames(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"linux", "mac", "explicit-false", "capture-error", "true-with-capture-error", "true-with-domain-error", "wrong-size", "missing-capture", "domain"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name != "get_window_state" {
					return Reply{}, nil, false
				}
				wire := map[string]any{"pid": args["pid"], "window_id": args["window_id"], "snapshot_id": "s12345678", "capture_id": "capture", "screenshot_width": 2100, "screenshot_height": 2}
				switch kind {
				case "explicit-false":
					wire["screenshot_frame_valid"] = false
				case "capture-error", "true-with-capture-error":
					wire["screenshot_error"] = map[string]any{"code": "identity_unproven"}
				case "wrong-size":
					wire["screenshot_width"] = 100
				case "missing-capture":
					delete(wire, "capture_id")
				}
				if strings.HasPrefix(kind, "true-with-") {
					wire["screenshot_frame_valid"] = true
				}
				reply := structuredReply(wire)
				reply.Images = []Image{{Data: pixelFixture(t), MIMEType: "image/png"}}
				reply.IsError = kind == "domain" || kind == "true-with-domain-error"
				return reply, nil, true
			}}
			m, r := testRun(t, f, true)
			if kind != "mac" {
				m.platform = "linux"
			}
			o := observed(t, r, true)
			_, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, Point: &Point{X: 10, Y: 0}})
			wantCalls := 0
			if kind == "linux" {
				wantCalls = 1
			}
			if (err == nil) != (kind == "linux") || f.count("click") != wantCalls {
				t.Fatal("capture contract mismatch", kind, err, f.count("click"))
			}
		})
	}
}

func TestLinuxSemanticProjectionCannotProveMissingPassiveText(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
		if name != "get_window_state" {
			return Reply{}, nil, false
		}
		return structuredReply(map[string]any{"pid": args["pid"], "window_id": args["window_id"], "elements_complete": true,
			"elements": []Element{{Role: "button", Label: "Commit"}}}), nil, true
	}}
	m, r := testRun(t, f, false)
	m.platform = "linux"
	o := observed(t, r, false)
	if o.Complete || semanticCondition(o, "missing passive label") != "unknown" {
		t.Fatal("actionable-only projection claimed complete visible text", o)
	}
	if semanticCondition(o, "Commit") != "satisfied" {
		t.Fatal("positive semantic evidence was lost")
	}
}

func TestLinuxDoesNotReuseMacForegroundRefusalContract(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{handle: func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "type_text" {
			return backgroundRefusal(), nil, true
		}
		return Reply{}, nil, false
	}}
	m, r := testRun(t, f, false)
	m.platform = "linux"
	r.options.Mode = ForegroundAllowed
	o := observed(t, r, false)
	request := ActRequest{Kind: "type_text", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token, Text: "synthetic"}
	result, err := r.Act(t.Context(), request)
	if err != nil || !result.DriverError || result.Observation == nil || result.Observation.ForegroundAction != "" {
		t.Fatal("Linux refusal inferred macOS foreground eligibility", result, err)
	}
	request.ObservationRef = result.Observation.Ref
	request.ElementToken = result.Observation.Elements[0].Token
	request.DeliveryMode = "foreground"
	if _, err := r.Act(t.Context(), request); err == nil || f.count("type_text") != 1 {
		t.Fatal("unreviewed foreground input dispatched", err)
	}
}

func TestLinuxLaunchBindsDiscoveredCommandAndConfirmsNativeIdentity(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ready", "missing-command", "foreign-response", "missing-running"} {
		t.Run(mode, func(t *testing.T) {
			path := "/usr/bin/synthetic --fixture"
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				switch name {
				case "list_apps":
					command := path
					if mode == "missing-command" {
						command = ""
					}
					return structuredReply(map[string]any{"apps": []any{map[string]any{"name": "Editor", "bundle_id": "synthetic-editor", "launch_path": command, "running": false}}}), nil, true
				case "launch_app":
					if len(args) != 1 || args["launch_path"] != path {
						t.Fatal("launch did not round-trip the discovered XDG command", args)
					}
					name := path
					if mode == "foreign-response" {
						name = "different-command"
					}
					return structuredReply(map[string]any{"pid": 41, "name": name, "bundle_id": nil, "running": mode != "missing-running", "windows": []any{map[string]any{"pid": 41, "window_id": 99}}}), nil, true
				}
				return Reply{}, nil, false
			}}
			m, r := testRun(t, f, false)
			m.platform = "linux"
			discovery, err := r.Apps(t.Context(), "", 8)
			if err != nil || len(discovery.Apps) != 1 {
				t.Fatal(discovery, err)
			}
			if mode == "missing-command" {
				if discovery.Apps[0].Ref != "" {
					t.Fatal("unlaunchable process received app ref")
				}
				return
			}
			request := ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref}
			result, err := r.Act(t.Context(), request)
			if err != nil || !result.Dispatched || (result.Observation != nil) != (mode == "ready") || (result.ObservationError == "") != (mode == "ready") {
				t.Fatal(result, err)
			}
			if _, err := r.Act(t.Context(), request); err == nil || f.count("launch_app") != 1 {
				t.Fatal("launch was repeated")
			}
		})
	}
}

func TestLinuxAdmissionRequiresKnownX11AndRefusesWayland(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		facts LinuxInspection
		code  string
	}{
		{"x11", LinuxInspection{X11: PermissionGranted, WaylandEnvironment: PermissionMissing, WaylandBackend: PermissionMissing}, ""},
		{"headless", LinuxInspection{X11: PermissionMissing, WaylandEnvironment: PermissionMissing, WaylandBackend: PermissionMissing}, "display_unavailable"},
		{"xwayland", LinuxInspection{X11: PermissionGranted, WaylandEnvironment: PermissionGranted, WaylandBackend: PermissionMissing}, "display_unsupported"},
		{"wayland-backend", LinuxInspection{X11: PermissionGranted, WaylandEnvironment: PermissionMissing, WaylandBackend: PermissionGranted}, "display_unsupported"},
		{"unknown", LinuxInspection{}, "display_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireLinuxX11(tc.facts)
			if (err == nil) != (tc.code == "") || (tc.code != "" && !serviceHasCode(err, tc.code)) {
				t.Fatal(err)
			}
		})
	}
}

func TestLinuxFullSchemasRejectDriftBeforeCalls(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"linux-full", "linux-full-drift"} {
		transport, peer := fakeTransport(t, mode)
		c, err := connectReviewed(t.Context(), transport, reviewedLinuxTools)
		if strings.HasSuffix(mode, "drift") {
			if !serviceHasCode(err, "incompatible_service") || peer.calls.Load() != 0 {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		defer c.close()
		if len(c.tools) != 15 {
			t.Fatal("unexpected Linux capabilities", len(c.tools))
		}
		if _, err := c.call(t.Context(), "kill_app", nil); err == nil || peer.calls.Load() != 0 {
			t.Fatal("unreviewed tool reached upstream")
		}
	}
}

func linuxFullSchemaFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var inventory schemaInventory
	if err := json.Unmarshal(linuxSchemaInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory.Tools
}
