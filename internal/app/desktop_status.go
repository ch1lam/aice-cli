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

// desktopStatusField does no work under the Settings lifecycle/state locks.
// Opening or refreshing the panel triggers one bounded read, never a poll loop.
func (s *interactiveSession) desktopStatusField(ctx context.Context, settings interactiveSettings) interaction.SettingField {
	field := interaction.SettingField{ID: "desktop.status", Category: "tools", Label: "Computer Use status", Kind: interaction.SettingInfo, Keywords: []string{"desktop", "cua", "permissions", "capture"}}
	summary, connection := "Unknown", "Unknown"
	permissions := desktop.Inspection{Accessibility: desktop.PermissionUnknown, ScreenRecording: desktop.PermissionUnknown}
	var err error
	var cached desktop.Status
	var setupCapture time.Time
	if runtime.GOOS != "darwin" {
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
	if permissions.ConnectionVerified {
		connection, summary = "Verified", "Connected; capture not checked"
		if permissions.Accessibility != desktop.PermissionGranted || permissions.ScreenRecording != desktop.PermissionGranted {
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
				summary = "Ready at last capture check"
			} else {
				summary = "Degraded"
			}
		}
	}
	images := slices.Contains(settings.model.InputModalities, llm.InputModalityImage)
	modelCapability := "Semantic and image input"
	if !images {
		modelCapability = "Semantic only; current model cannot receive images or use pixel actions"
		if permissions.ConnectionVerified && summary != "Needs setup" && err == nil {
			summary = "Degraded"
		}
	}
	if !settings.configuration.DesktopEnabled {
		summary = "Disabled"
	}
	lines := []string{
		"State: " + summary,
		"Connection: " + connection,
		"Accessibility: " + string(permissions.Accessibility),
		"Screen Recording: " + string(permissions.ScreenRecording),
		"Capture verification: " + capture,
		"Model: " + modelCapability,
		fmt.Sprintf("This instance's tool connection: %t (generation %d)", cached.Connected, cached.Generation),
		"Refresh reads status only; it never captures, requests grants or starts a service.",
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
