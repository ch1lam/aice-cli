package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestWindowsInspectionIdentityAndFacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"medium", "unavailable-integrity", "uiaccess-unknown", "session-zero", "absent", "restricted", "foreign-peer", "changed-peer", "changed-pid", "pid-reused", "changed-session", "wrong-version", "wrong-platform", "missing-fact", "domain", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			calls, peers, probes := 0, 0, 0
			facts := map[string]any{"elevated": false, "integrity_level": "Medium", "integrity_level_rid": 0x2000, "uia": true, "post_message": true, "extra": "private-must-not-be-projected"}
			if kind == "unavailable-integrity" {
				facts["integrity_level"], facts["integrity_level_rid"] = "Unavailable", nil
			}
			if kind == "missing-fact" {
				delete(facts, "uia")
			}
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				calls++
				if len(args) != 0 {
					t.Fatal("Windows status sent unsupported permission arguments")
				}
				switch name {
				case "get_config":
					version, platform := DriverVersion, "windows"
					if kind == "wrong-version" {
						version = "0.0.0"
					}
					if kind == "wrong-platform" {
						platform = "macos"
					}
					return structuredReply(map[string]string{"version": version, "platform": platform}), nil, true
				case "check_permissions":
					if kind == "cancel" {
						return Reply{}, context.Canceled, true
					}
					r := structuredReply(facts)
					r.IsError = kind == "domain"
					return r, nil, true
				default:
					t.Fatalf("status dispatched %s", name)
					return Reply{}, nil, true
				}
			}}
			inspector := windowsInspector{
				service: &serviceConnector{endpoint: `\\.\pipe\cua-driver`, status: func(context.Context) (string, error) {
					probes++
					if kind == "absent" {
						return "", serviceError("not_running", "absent")
					}
					output := serviceStatusFixture(`\\.\pipe\cua-driver`)
					if kind == "restricted" {
						output = strings.Replace(output, "standard", "unrestricted", 1)
					}
					if kind == "changed-pid" && probes == 2 {
						output = strings.Replace(output, "4321", "1234", 1)
					}
					return output, nil
				}},
				peer: func(context.Context, int) (windowsServicePeer, error) {
					peers++
					if kind == "foreign-peer" || kind == "changed-peer" && peers == 2 {
						return windowsServicePeer{}, serviceError("identity_mismatch", "foreign")
					}
					identity := windowsServicePeer{sessionID: 2, created: 123, uiAccess: PermissionMissing}
					if kind == "session-zero" {
						identity.sessionID = 0
					}
					if kind == "uiaccess-unknown" {
						identity.uiAccess = PermissionUnknown
					}
					if kind == "pid-reused" && peers == 2 {
						identity.created++
					}
					if kind == "changed-session" && peers == 2 {
						identity.sessionID++
					}
					return identity, nil
				},
				connect: func(context.Context) (driverClient, error) { return f, nil },
			}
			report, err := inspector.inspect(t.Context())
			accepted := kind == "medium" || kind == "unavailable-integrity" || kind == "uiaccess-unknown" || kind == "session-zero"
			if (err == nil) != accepted || report.ConnectionVerified != accepted || report.CheckedAt.IsZero() {
				t.Fatal(report, err)
			}
			if report.Accessibility != PermissionUnknown || report.ScreenRecording != PermissionUnknown || report.Linux != nil {
				t.Fatal("Windows invented another platform's grants")
			}
			if accepted {
				if calls != 2 || probes != 2 || peers != 2 || report.Windows.SessionID == nil || report.Windows.UIAReported != PermissionGranted {
					t.Fatal("incomplete status admission", report)
				}
				if kind == "unavailable-integrity" && (report.Windows.IntegrityRID != nil || report.Windows.IntegrityLevel != "Unavailable") {
					t.Fatal("missing integrity became a lower privilege claim")
				}
				if kind == "uiaccess-unknown" && report.Windows.UIAccess != PermissionUnknown {
					t.Fatal("unknown UIAccess became missing")
				}
				if kind == "session-zero" && *report.Windows.SessionID != 0 {
					t.Fatal("services session hidden")
				}
			} else if report.Windows.SessionID != nil || report.Windows.UIAReported != PermissionUnknown {
				t.Fatal("unverified service facts published")
			}
			if calls > 0 && f.closed != 1 {
				t.Fatal("inspection leaked its proxy")
			}
			if (kind == "foreign-peer" || kind == "restricted" || kind == "absent") && calls != 0 {
				t.Fatal("unverified service received MCP calls")
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation cause lost", err)
			}
			data, err := json.Marshal(report)
			if err != nil || strings.Contains(string(data), "private-must-not-be-projected") {
				t.Fatal("native details leaked into public status", err)
			}
		})
	}
}

func TestWindowsPermissionFactsRejectContradictions(t *testing.T) {
	t.Parallel()
	for _, facts := range []string{
		`{"elevated":false,"integrity_level":"High","integrity_level_rid":12288,"uia":true,"post_message":true}`,
		`{"elevated":true,"integrity_level":"Unavailable","integrity_level_rid":null,"uia":true,"post_message":true}`,
		`{"elevated":false,"integrity_level":"Medium","integrity_level_rid":null,"uia":true,"post_message":true}`,
		`{"elevated":false,"integrity_level":"Medium","integrity_level_rid":8192,"uia":"true","post_message":true}`,
	} {
		t.Run(facts, func(t *testing.T) {
			if _, err := readWindowsPermissions(Reply{Structured: json.RawMessage(facts)}); !serviceHasCode(err, "permissions_unknown") {
				t.Fatal("contradictory or malformed facts accepted", err)
			}
		})
	}
}

func TestWindowsInspectionDeadlineCoversPeerProbe(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		inspector := windowsInspector{
			service: &serviceConnector{endpoint: "local", status: func(context.Context) (string, error) { return serviceStatusFixture("local"), nil }},
			peer: func(ctx context.Context, _ int) (windowsServicePeer, error) {
				<-ctx.Done()
				return windowsServicePeer{}, ctx.Err()
			},
			connect: func(context.Context) (driverClient, error) { t.Fatal("connected after peer timeout"); return nil, nil },
		}
		started := time.Now()
		_, err := inspector.inspect(t.Context())
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 5*time.Second {
			t.Fatal("unbounded inspection", err, time.Since(started))
		}
	})
}

func TestWindowsStatusClientAdmitsOnlyReviewedTools(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"windows-status", "windows-status-drift"} {
		t.Run(mode, func(t *testing.T) {
			transport, peer := fakeTransport(t, mode)
			client, err := connectReviewed(t.Context(), transport, reviewedWindowsStatusTools)
			if mode == "windows-status-drift" {
				if !serviceHasCode(err, "incompatible_service") || peer.calls.Load() != 0 {
					t.Fatal("schema drift reached a tool", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			if len(client.tools) != 2 {
				t.Fatal("extra status tools admitted")
			}
			for _, tool := range []string{"click", "get_window_state", "start_session", "set_config", "unreviewed_tool"} {
				if _, err := client.call(t.Context(), tool, nil); err == nil || peer.calls.Load() != 0 {
					t.Fatal("status dispatched an unreviewed tool")
				}
			}
		})
	}
}

func windowsStatusFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var inventory schemaInventory
	if err := json.Unmarshal(windowsStatusSchemaInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory.Tools
}
