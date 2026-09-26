package app

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestDesktopSettingsReadOnlyStatusFacts(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS status projection")
	}
	for _, kind := range []string{"disabled", "absent", "stopped", "missing", "granted", "text-model", "captured", "capture-failed", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s := desktopSettingsSession(t)
			s.configuration.DesktopEnabled = kind != "disabled"
			s.model.InputModalities = []llm.InputModality{llm.InputModalityImage}
			if kind == "text-model" {
				s.model.InputModalities = nil
			}
			calls := 0
			s.desktop.inspect = func(ctx context.Context) (desktop.Inspection, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 8*time.Second {
					t.Fatal("unbounded status read")
				}
				if kind == "absent" {
					return desktop.Inspection{}, deps.ErrCuaNotInstalled
				}
				if kind == "stopped" {
					return desktop.Inspection{}, &desktop.ServiceError{Code: "not_running", Detail: "stopped"}
				}
				if kind == "unknown" {
					return desktop.Inspection{}, errors.New("synthetic failure")
				}
				report := desktop.Inspection{ConnectionVerified: true, Accessibility: desktop.PermissionGranted, ScreenRecording: desktop.PermissionGranted, CheckedAt: time.Now()}
				if kind == "missing" {
					report.ScreenRecording = desktop.PermissionMissing
				}
				return report, nil
			}
			s.desktop.install = func(context.Context, deps.Options) (deps.CuaInstallResult, error) {
				t.Fatal("status installed a helper")
				return deps.CuaInstallResult{}, nil
			}
			s.desktop.setup = func(context.Context, string, desktop.SetupOptions) (desktop.SetupResult, error) {
				t.Fatal("status requested grants")
				return desktop.SetupResult{}, nil
			}
			s.desktop.status = func() desktop.Status {
				if kind == "captured" || kind == "capture-failed" {
					return desktop.Status{CaptureCheckedAt: time.Unix(100, 0), CaptureAvailable: kind == "captured"}
				}
				return desktop.Status{}
			}
			// Status remains readable during a run and does not take a write reservation.
			s.lifecycle.mainRunning = true
			snapshot, err := s.ReadSettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var field interaction.SettingField
			for _, f := range snapshot.Fields {
				if f.ID == "desktop.status" {
					field = f
				}
			}
			want := map[string]string{"disabled": "Disabled", "absent": "Not installed", "stopped": "Needs setup", "missing": "Needs setup", "granted": "Connected; capture not checked", "text-model": "Degraded", "captured": "Ready at last capture check", "capture-failed": "Degraded", "unknown": "Unavailable"}[kind]
			if calls != 1 || field.Value.Text != want || snapshot.Revision != 0 || s.conversation.store != nil || field.DisabledReason != "" {
				t.Fatalf("status=%+v calls=%d", field, calls)
			}
			if kind == "captured" && !strings.Contains(field.Description, "historical result, not a guarantee") {
				t.Fatal("historical capture became permanent readiness")
			}
			if kind == "unknown" && !strings.Contains(field.Description, "Accessibility: Unknown") {
				t.Fatal("unknown permission became missing")
			}
		})
	}
}

func TestDesktopSettingsStatusCancellation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS status projection")
	}
	s := desktopSettingsSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.desktop.inspect = func(ctx context.Context) (desktop.Inspection, error) {
		cancel()
		<-ctx.Done()
		return desktop.Inspection{}, ctx.Err()
	}
	if _, err := s.ReadSettings(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read published a snapshot", err)
	}
}

func TestDesktopLinuxStatusDoesNotInventCaptureOrMacGrants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux and macOS status reader")
	}
	s := desktopSettingsSession(t)
	s.configuration.DesktopEnabled = true
	s.desktop.inspect = func(context.Context) (desktop.Inspection, error) {
		return desktop.Inspection{ConnectionVerified: true, Linux: &desktop.LinuxInspection{X11: desktop.PermissionMissing, ATSPI: desktop.PermissionGranted, WaylandEnvironment: desktop.PermissionGranted, WaylandBackend: desktop.PermissionMissing}}, nil
	}
	field := s.desktopStatusField(t.Context(), s.settingsSnapshot())
	if field.Value.Text != "Unavailable" || !strings.Contains(field.Description, "Connection: Verified") || !strings.Contains(field.Description, "X11 connection: Unavailable") || !strings.Contains(field.Description, "AT-SPI bus owner: Available") || !strings.Contains(field.Description, "Capture verification: Not checked") || strings.Contains(field.Description, "Screen Recording:") {
		t.Fatal(field)
	}
}

func TestDesktopLinuxStatusSeparatesOwnedConnectionFromSharedInspection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux and macOS status reader")
	}
	for _, kind := range []string{"x11", "no-atspi", "idle", "owned", "owned-captured", "restricted"} {
		t.Run(kind, func(t *testing.T) {
			s := desktopSettingsSession(t)
			s.configuration.DesktopEnabled = true
			s.model.InputModalities = []llm.InputModality{llm.InputModalityImage}
			s.desktop.inspect = func(context.Context) (desktop.Inspection, error) {
				facts := &desktop.LinuxInspection{X11: desktop.PermissionGranted, ATSPI: desktop.PermissionGranted, WaylandEnvironment: desktop.PermissionMissing, WaylandBackend: desktop.PermissionMissing}
				if kind == "idle" || strings.HasPrefix(kind, "owned") {
					return desktop.Inspection{Linux: &desktop.LinuxInspection{}}, &desktop.ServiceError{Code: "not_running", Detail: "shared service absent"}
				}
				if kind == "restricted" {
					return desktop.Inspection{Linux: &desktop.LinuxInspection{}}, &desktop.ServiceError{Code: "external_restriction", Detail: "shared service restricted"}
				}
				if kind == "no-atspi" {
					facts.ATSPI = desktop.PermissionMissing
				}
				return desktop.Inspection{ConnectionVerified: true, Linux: facts}, nil
			}
			s.desktop.status = func() desktop.Status {
				status := desktop.Status{Connected: strings.HasPrefix(kind, "owned") || kind == "restricted"}
				if kind == "owned-captured" {
					status.CaptureCheckedAt, status.CaptureAvailable = time.Unix(100, 0), true
				}
				return status
			}
			field := s.desktopStatusField(t.Context(), s.settingsSnapshot())
			want := map[string]string{"x11": "Connected; capture not checked", "no-atspi": "Degraded", "idle": "Idle; connects on first use", "owned": "Connected (cached)", "owned-captured": "Connected (cached)", "restricted": "Unavailable"}[kind]
			if field.Value.Text != want || strings.Contains(field.Description, "Screen Recording:") || !strings.Contains(field.Description, "describe the shared service") {
				t.Fatal(field)
			}
		})
	}
}
