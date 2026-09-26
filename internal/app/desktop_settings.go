package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const desktopDisclosure = "Computer Use can access applications and data outside this project. Task window content may be sent to the current model provider. GUI actions affect real applications. Enabling is global; ordinary application switching will not ask again."

const desktopSetupDisclosure = "Setup may download the pinned Cua Driver and install its signed App in /Applications. It requests Accessibility and Screen Recording for CuaDriver, then explicitly tests direct capture. macOS may show an additional screen access picker; only you can approve it. AICE disables telemetry and update checks for children it starts, and leaves shared Driver preferences unchanged. Cancelling cannot undo installation or grants already completed."

func desktopSetupCommand() *interaction.Command {
	return &interaction.Command{Name: "desktop", Interactive: true, Menu: &interaction.CommandMenu{Title: "Computer Use", Options: []interaction.CommandOption{
		{Label: "Set up / Repair", Arguments: "setup", Description: "Install, request OS permissions and verify"},
		{Label: "Enable preference only", Arguments: "enable", Description: "Save without installation or OS prompts; readiness stays unknown"},
	}}}
}

// runDesktopSettings runs under RunSettingsAction's single shared reservation.
// External work and preference publication retain separate success facts.
func (s *interactiveSession) runDesktopSettings(ctx context.Context, request interaction.CommandRequest) (result interaction.SettingsActionResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	action := strings.TrimSpace(request.Arguments)
	if action != "setup" && action != "enable" {
		return result, errors.New("app: choose Computer Use setup or enable")
	}
	ui := request.Auth
	if ui == nil || ui.Notify == nil || ui.Input == nil {
		return result, errors.New("app: Computer Use setup requires the Settings confirmation dialog")
	}
	if s.desktop == nil {
		return result, errors.New("app: Computer Use runtime is unavailable")
	}
	instructions := desktopDisclosure
	if action == "setup" {
		if s.desktop.install == nil || s.desktop.setup == nil {
			return result, errors.New("app: Computer Use setup is unavailable")
		}
		instructions += "\n\n" + desktopSetupDisclosure
		if s.desktop.installOptions.NoInstall {
			instructions += "\n\nHelper downloads are disabled for this instance. A compatible installed App can be reused. Changes to Allow helper downloads apply after restarting AICE."
		}
	}
	if err := ui.Notify(ctx, interaction.AuthPrompt{Title: "Enable Computer Use", Instructions: instructions, Menu: &interaction.CommandMenu{Title: "Continue?", Options: []interaction.CommandOption{
		{Label: "Cancel", Arguments: "cancel"}, {Label: "Continue", Arguments: "continue"},
	}}}); err != nil {
		return result, err
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case answer, ok := <-ui.Input:
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !ok || answer != "continue" {
			result.Output = "Computer Use setup cancelled; no changes made"
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	notify := func(text string) error {
		return ui.Notify(ctx, interaction.AuthPrompt{Title: "Computer Use setup", Instructions: text})
	}
	if action == "setup" {
		if err := notify("Verifying the signed Cua Driver installation…"); err != nil {
			return result, err
		}
		// The policy was captured at startup. A restart-only preference saved in
		// this session must not silently authorize a new download.
		lastMiB := int64(-1)
		options := s.desktop.installOptions.WithProgress(func(p deps.Progress) error {
			mib := p.Downloaded / (1 << 20)
			if mib == lastMiB && p.Downloaded != p.Total {
				return ctx.Err()
			}
			lastMiB = mib
			return notify(fmt.Sprintf("Downloading Cua Driver %s: %.1f MiB received", p.Version, float64(p.Downloaded)/(1<<20)))
		})
		installed, err := s.desktop.install(ctx, options)
		result.Warnings = append(result.Warnings, installed.Warnings...)
		if installed.Installed || installed.Reused {
			verb := "Verified existing signed App"
			if installed.Installed {
				verb = "Installed and verified signed App"
			}
			result.External = append(result.External, interaction.SettingsActionStep{Name: "Driver installation", Detail: verb, Completed: true})
		}
		if err != nil {
			return result, err
		}
		if installed.Installation.Binary == "" {
			return result, errors.New("app: verified Driver installation is missing its binary")
		}
		if err := notify("Opening CuaDriver's system permission flow. Approve CuaDriver in macOS, then return here. Direct capture will be tested. Esc cancels this wait; system dialogs may remain open."); err != nil {
			return result, err
		}
		native, err := s.desktop.setup(ctx, installed.Installation.Binary)
		if native.AuthorizationCompleted {
			s.desktop.healthMu.Lock()
			s.desktop.setupCaptureAt = time.Now()
			s.desktop.healthMu.Unlock()
		}
		if native.LaunchRequested {
			result.External = append(result.External, interaction.SettingsActionStep{Name: "Service launch", Detail: "Launch requested; the shared daemon is not owned by AICE", Completed: native.Ready})
		}
		if native.AuthorizationRequested {
			result.External = append(result.External, interaction.SettingsActionStep{Name: "OS authorization and live capture check", Detail: "Completed grants are not rolled back on cancellation", Completed: native.AuthorizationCompleted})
		}
		result.ReadinessKnown, result.Ready = true, native.Ready
		if err != nil {
			return result, err
		}
		if !native.Ready {
			return result, errors.New("app: native setup did not establish readiness")
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !slices.Contains(s.settingsSnapshot().model.InputModalities, llm.InputModalityImage) {
		result.Ready = false
		result.Warnings = append(result.Warnings, "The current model has no image input. Semantic tools remain available; screenshot and pixel actions require an image-capable model.")
	}
	saved, err := s.applySettingsReserved(ctx, interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: true}}}}, true, interaction.SettingNextRun)
	result.Committed, result.Applied = saved.Committed, saved.Applied
	result.Warnings = append(result.Warnings, saved.Warnings...)
	if err != nil {
		result.Output = "Saving the enabled preference failed. Use Enable preference only to retry saving."
		if len(result.External) > 0 {
			result.Output = "External setup steps are retained. " + result.Output
		}
		return result, err
	}
	result.Output = "Computer Use enabled for the next run"
	result.Continuation = s.desktopContinuation()
	return result, nil
}
