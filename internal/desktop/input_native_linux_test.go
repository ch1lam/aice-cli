//go:build integration && linux

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Native input evidence for the reviewed X11 route. Coordinates come from the
// synthetic fixture's geometry, not a visual model. Never run on a user display.
// The full gate currently fails for Unicode insertion and unavailable GTK key
// delivery; keep the requested postconditions (docs/desktop.md), not xfails.
func TestNativeLinuxInput(t *testing.T) {
	if os.Getenv("AICE_CUA_X11_CONTAINER") != "1" {
		t.Skip("requires the explicitly isolated Linux X11 fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Getenv("DISPLAY") != ":99" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("requires the test container's Xvfb :99 and private session bus")
	}
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("requires explicitly supplied checksum-verified Driver")
	}
	for _, kind := range []string{"type_text_ascii", "type_text_unicode", "key", "hotkey", "pixel_click", "pixel_resize"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			home := t.TempDir()
			t.Setenv("HOME", home)
			script := filepath.Join(home, "fixture.py")
			if err := os.WriteFile(script, linuxFixtureScript, 0600); err != nil {
				t.Fatal(err)
			}
			target := startLinuxProbeFixture(t, ctx, script, "AICE Native Input Target", "input")
			sentinel := startLinuxProbeFixture(t, ctx, script, "AICE Native Input Sentinel", "sentinel")
			awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active })
			m, err := NewManager(func(context.Context) (string, string, error) { return binary, filepath.Join(home, "cua.sock"), nil })
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := m.Close(); err != nil {
					t.Error(err)
				}
			}()
			r, err := m.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
			discovery, err := r.Windows(ctx, target.name, 16)
			if err != nil {
				t.Fatal(err)
			}
			var selected Window
			for _, window := range discovery.Windows {
				if window.PID == target.pid && window.Title == target.name {
					selected = window
				}
			}
			if selected.Ref == "" {
				t.Fatal("exact synthetic target missing")
			}
			obs, err := r.Observe(ctx, ObserveRequest{TargetRef: selected.Ref, Screenshot: true})
			if err != nil || obs.Image == nil || obs.Degraded {
				t.Fatal("initial native observation unavailable", err)
			}
			// Establish empty contents before testing insertion independently.
			if strings.HasPrefix(kind, "type_text") || kind == "key" {
				seed, err := r.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: obs.Ref, ElementToken: (linuxProbeObservation{Elements: obs.Elements}).token(t, "Task value"), Screenshot: true})
				linuxManagerReturned(t, r, seed, err)
				obs = *seed.Observation
				awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Value == "" })
			}
			if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.KeysSent >= 3 })
			before := awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Width > 0 && s.Height > 0 })
			request := ActRequest{Kind: kind, ObservationRef: obs.Ref, Screenshot: true}
			switch kind {
			case "type_text_ascii", "type_text_unicode":
				request.Kind = "type_text"
				request.Text, request.ElementToken = "Native ASCII", (linuxProbeObservation{Elements: obs.Elements}).token(t, "Task value")
				if kind == "type_text_unicode" {
					request.Text = "Native 中文 ✓"
				}
			case "key":
				request.Key, request.ElementToken = "x", (linuxProbeObservation{Elements: obs.Elements}).token(t, "Task value")
			case "hotkey":
				request.Keys, request.ElementToken = []string{"ctrl", "a"}, (linuxProbeObservation{Elements: obs.Elements}).token(t, "Task value")
			default:
				request.Kind = "click"
				request.Point = &Point{X: before.ButtonX * float64(obs.ImageWidth) / float64(before.Width), Y: before.ButtonY * float64(obs.ImageHeight) / float64(before.Height)}
				if kind == "pixel_resize" {
					if err := os.WriteFile(filepath.Join(target.directory, "resize"), nil, 0600); err != nil {
						t.Fatal(err)
					}
					awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Width == 900 && s.Height == 350 })
				}
			}
			started := time.Now()
			result, err := r.Act(ctx, request)
			var facts struct{ Code, Effect string }
			_ = json.Unmarshal(result.Driver, &facts)
			t.Logf("native action=%s elapsed=%s outcome=%s driver_error=%v code=%s effect=%s", kind, time.Since(started), result.Outcome, result.DriverError, facts.Code, facts.Effect)
			// Read a settled application state even when the Driver refused input.
			// An honest refusal is evidence, but it must not pass the input gate.
			afterReply := awaitLinuxProbeState(t, ctx, target, func(linuxProbeState) bool { return true })
			settled := awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Ticks > afterReply.Ticks+3 })
			if kind == "pixel_resize" {
				if err != nil || !result.DriverError || facts.Code != "capture_frame_mismatch" || facts.Effect != "refused" || result.Observation == nil || settled.Commits != 0 {
					t.Error("old image geometry was not refused before input", err)
				} else {
					// Use the newly returned screenshot, never replay the old point.
					fresh := result.Observation
					point := &Point{X: settled.ButtonX * float64(fresh.ImageWidth) / float64(settled.Width), Y: settled.ButtonY * float64(fresh.ImageHeight) / float64(settled.Height)}
					next, err := r.Act(ctx, ActRequest{Kind: "click", ObservationRef: fresh.Ref, Point: point, Screenshot: true})
					linuxManagerReturned(t, r, next, err)
					awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
				}
			} else {
				if err != nil || result.DriverError || result.Observation == nil || result.Observation.Image == nil || result.ObservationError != "" {
					t.Error("native input acceptance failed: action or follow-up observation unavailable", err)
				}
				matched := false
				switch kind {
				case "type_text_ascii", "type_text_unicode":
					matched = settled.Value == request.Text
					t.Logf("text readback: requested_bytes=%d requested_runes=%d actual_bytes=%d actual_runes=%d proper_prefix=%v", len(request.Text), utf8.RuneCountInString(request.Text), len(settled.Value), utf8.RuneCountInString(settled.Value), !matched && strings.HasPrefix(request.Text, settled.Value))
				case "key":
					matched = settled.Value == "x"
				case "hotkey":
					matched = slices.Equal(settled.Selection, []int{0, len("AICE-314")})
				default:
					matched = settled.Commits == 1 && settled.Result == "Result: AICE-314"
				}
				if !matched {
					t.Error("independent application state did not satisfy requested input")
				}
			}
			if err := os.WriteFile(filepath.Join(sentinel.directory, "stop-typing"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			final := awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Value == strings.Repeat("a", s.KeysSent) })
			if !final.Active || final.FocusLosses != 0 || final.Commits != 0 {
				t.Fatal("native input disturbed foreground sentinel")
			}
			t.Logf("native action=%s foreground sentinel: concurrent_keys=%d focus_losses=0", kind, final.KeysSent)
		})
	}
}
