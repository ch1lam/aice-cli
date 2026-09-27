package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

var _ interaction.SettingsStatusReader = (*interactiveSession)(nil)

func desktopStatusPlaceholder() interaction.SettingField {
	return interaction.SettingField{ID: "desktop.status", Category: "tools", Label: "Computer Use status", Kind: interaction.SettingInfo,
		Keywords: []string{"desktop", "cua", "permissions", "capture"}, Value: interaction.SettingValue{Text: "Checking…"},
		Description: "Checking Computer Use status in the background. Other settings are available."}
}

// ReadSettingsStatus performs one bounded inspection outside the preference
// read path and all lifecycle/state locks. It never reserves a writer or polls.
func (s *interactiveSession) ReadSettingsStatus(ctx context.Context, revision uint64) (interaction.SettingField, error) {
	field := desktopStatusPlaceholder()
	if err := ctx.Err(); err != nil {
		return field, err
	}
	s.lifecycle.mu.Lock()
	settings := s.settingsSnapshot()
	current := s.lifecycle.revision
	s.lifecycle.mu.Unlock()
	if revision != current {
		return field, interaction.ErrSettingsStale
	}
	status := s.desktopStatusField(ctx, settings)
	if err := ctx.Err(); err != nil {
		return field, err
	}
	if current, _ := s.settingsStatus(); revision != current {
		return field, interaction.ErrSettingsStale
	}
	return status, nil
}

func (s *interactiveSession) desktopStatusField(ctx context.Context, settings interactiveSettings) interaction.SettingField {
	field := desktopStatusPlaceholder()
	summary, connection := "Unknown", "Unknown"
	permissions := desktop.Inspection{Accessibility: desktop.PermissionUnknown, ScreenRecording: desktop.PermissionUnknown}
	var err error
	var cached desktop.Status
	var setupCapture time.Time
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		summary = "Unavailable"
		err = errors.New("Native Computer Use status is not yet integrated on this platform")
	} else if s.desktop == nil || s.desktop.inspect == nil {
		err = errors.New("Computer Use status reader is unavailable")
	} else {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		permissions, err = s.desktop.inspect(probeCtx)
		cancel()
		if permissions.Accessibility == "" {
			permissions.Accessibility = desktop.PermissionUnknown
		}
		if permissions.ScreenRecording == "" {
			permissions.ScreenRecording = desktop.PermissionUnknown
		}
	}
	if s.desktop != nil {
		if s.desktop.status != nil {
			cached = s.desktop.status()
		}
		s.desktop.healthMu.Lock()
		setupCapture = s.desktop.setupCaptureAt
		s.desktop.healthMu.Unlock()
	}
	windows := permissions.Windows != nil || runtime.GOOS == "windows"
	linux := !windows && (permissions.Linux != nil || runtime.GOOS == "linux")
	if windows {
		summary = "Unavailable"
	}
	if permissions.ConnectionVerified {
		connection, summary = "Verified", "Connected; capture not checked"
		if windows {
			summary = "Unavailable" // Status admission does not enable the Windows action runtime.
		} else if linux {
			facts := permissions.Linux
			if facts == nil || facts.X11 != desktop.PermissionGranted || facts.WaylandEnvironment != desktop.PermissionMissing || facts.WaylandBackend != desktop.PermissionMissing {
				summary = "Unavailable"
			} else if facts.ATSPI != desktop.PermissionGranted {
				summary = "Degraded"
			}
		} else if permissions.Accessibility != desktop.PermissionGranted || permissions.ScreenRecording != desktop.PermissionGranted {
			summary = "Needs setup"
		}
	}
	if err != nil {
		var native *desktop.ServiceError
		switch {
		case errors.Is(err, deps.ErrCuaNotInstalled):
			summary, connection = "Not installed", "Stopped"
		case errors.As(err, &native) && native.Code == "not_running":
			summary, connection = "Needs setup", "Stopped"
			if windows {
				summary = "Unavailable"
			} else if linux {
				summary = "Idle; connects on first use"
				if cached.Connected {
					summary = "Connected (cached)"
				}
			}
		case summary != "Unavailable":
			summary = "Unavailable"
		}
	}
	capture := "Not checked in this instance"
	checked, available := cached.CaptureCheckedAt, cached.CaptureAvailable
	if setupCapture.After(checked) {
		checked, available = setupCapture, true
	}
	if !checked.IsZero() {
		result := "Unavailable"
		if available {
			result = "Succeeded"
		}
		capture = result + " at " + checked.Format(time.RFC3339) + "; historical result, not a guarantee for the next capture"
		if summary == "Connected; capture not checked" {
			if available {
				summary = "Last capture succeeded"
			} else {
				summary = "Degraded"
			}
		}
	}
	images := slices.Contains(settings.model.InputModalities, llm.InputModalityImage)
	modelCapability := "Semantic and image input"
	if !images {
		modelCapability = "Semantic only; current model cannot receive images or use pixel actions"
		if permissions.ConnectionVerified && summary != "Needs setup" && summary != "Unavailable" && err == nil {
			summary = "Degraded"
		}
	}
	if !settings.configuration.DesktopEnabled {
		summary = "Disabled"
	}
	lines := []string{
		"State: " + summary,
		"Connection: " + connection,
		"Capture verification: " + capture,
		"Model: " + modelCapability,
		fmt.Sprintf("This instance's tool connection: %t (generation %d)", cached.Connected, cached.Generation),
		"Refresh reads status only; it never captures, requests grants or starts a service.",
	}
	if windows {
		facts := permissions.Windows
		if facts == nil {
			facts = &desktop.WindowsInspection{}
		}
		integrity, session := "Unknown", "Unknown"
		if facts.IntegrityLevel != "" {
			integrity = facts.IntegrityLevel
		}
		if facts.IntegrityRID != nil {
			integrity += fmt.Sprintf(" (RID 0x%04X)", *facts.IntegrityRID)
		}
		if facts.SessionID != nil {
			session = fmt.Sprint(*facts.SessionID)
			if *facts.SessionID == 0 {
				session += " (services; no interactive user desktop)"
			}
		}
		lines = append(lines,
			"Driver integrity level: "+integrity,
			"Driver UIAccess token: "+desktopCapabilityState(facts.UIAccess),
			"Driver Windows session: "+session,
			"Driver-reported UIA: "+desktopCapabilityState(facts.UIAReported),
			"Driver-reported PostMessage: "+desktopCapabilityState(facts.PostMessageReported),
			"These are shared-service identity and prerequisite facts, not verified target input or capture. A nonzero session does not prove an unlocked desktop.",
			"Windows setup and action runtime are not yet integrated. Status checks never elevate, change UIAccess, request UAC consent or start a service.")
	} else if linux {
		facts := permissions.Linux
		if facts == nil {
			facts = &desktop.LinuxInspection{}
		}
		lines = append(lines,
			"X11 connection: "+desktopCapabilityState(facts.X11),
			"AT-SPI bus owner: "+desktopCapabilityState(facts.ATSPI),
			"Wayland environment reported by Driver: "+desktopCapabilityState(facts.WaylandEnvironment),
			"Wayland backend enabled: "+desktopCapabilityState(facts.WaylandBackend),
			"XSendEvent prerequisite: "+desktopCapabilityState(facts.XSendEvent),
			"Connection and display facts above describe the shared service; this instance may instead own a private tool process.",
			"Display and bus checks do not verify target input or capture. X11 setup and actions are integrated; Wayland remains unavailable.",
			"Known Linux input limits: Unicode typing truncated text in native tests; GTK keyboard, pixel scrolling and dragging were unavailable. App launch took foreground focus.")
	} else {
		lines = append(lines, "Accessibility: "+string(permissions.Accessibility), "Screen Recording: "+string(permissions.ScreenRecording))
		if runtime.GOOS == "darwin" {
			lines = append(lines,
				"Known macOS input limits: Background dragging is unavailable; foreground dragging has not passed repeatability checks.",
				"Pixel double-click interrupted foreground focus in native tests; pixel right-click delivered twice.")
		}
	}
	if !permissions.CheckedAt.IsZero() {
		lines = append(lines, "Permissions read at: "+permissions.CheckedAt.Format(time.RFC3339))
	}
	if err != nil {
		lines = append(lines, "Status diagnostic: "+err.Error())
	}
	if cached.Diagnostic != "" {
		lines = append(lines, "Last tool connection diagnostic: "+cached.Diagnostic)
	}
	field.Value.Text, field.Description = summary, strings.Join(lines, "\n")
	return field
}

func desktopCapabilityState(state desktop.PermissionState) string {
	switch state {
	case desktop.PermissionGranted:
		return "Available"
	case desktop.PermissionMissing:
		return "Unavailable"
	default:
		return "Unknown"
	}
}
