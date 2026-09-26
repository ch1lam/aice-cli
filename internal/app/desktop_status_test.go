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
			s.desktop.setup = func(context.Context, string) (desktop.SetupResult, error) {
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
