package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const desktopServiceKey = "user:" + desktop.ServerID

const desktopDisclosure = "Computer Use adds the Cua Driver as the ordinary MCP service user:cua. Its tools can access applications and data outside this project, window content may be sent to the current model provider, and GUI actions affect real applications. Cua owns its permission mode, input delivery and concurrency; AICE still asks before each tool unless you grant it. Setup may download the pinned Cua Driver %s."

// desktopPreset installs the pinned Cua Driver and writes the ordinary MCP
// service that exposes it. It owns no runtime: Cua does, through MCP.
type desktopPreset struct {
	installOptions deps.Options
	install        func(context.Context, deps.Options) (deps.CuaInstallResult, error)
	// grant runs the OS permission flow; nil where the platform has none.
	grant func(context.Context, string) error
}

func (a *application) newDesktopPreset(configuration config.Config) *desktopPreset {
	if a.dependencies.newDesktop != nil {
		return a.dependencies.newDesktop(configuration)
	}
	home, err := a.userHome()
	if err != nil || home == "" {
		return nil
	}
	preset := &desktopPreset{
		installOptions: deps.DefaultOptions().WithBinDir(filepath.Join(home, ".aice", "bin")).WithNoInstall(configuration.NoDepInstall),
		install:        deps.InstallCua,
	}
	if desktop.GrantAvailable() {
		preset.grant = desktop.Grant
	}
	return preset
}

// runDesktopSettings runs under the shared Settings reservation; MCP changes
// replace resources only while idle. External steps are reported even when a
// later save fails.
func (s *interactiveSession) runDesktopSettings(ctx context.Context, request interaction.CommandRequest) (result interaction.SettingsActionResult, returnErr error) {
	action := strings.TrimSpace(request.Arguments)
	if action != "setup" && action != "disable" {
		return result, errors.New("app: choose Computer Use setup or disable")
	}
	s.stateMu.RLock()
	current, owner := s.configuration, s.mcp
	s.stateMu.RUnlock()
	if action == "disable" {
		if _, exists := current.MCP.Servers[desktopServiceKey]; !exists {
			result.Output = "Computer Use is not configured"
			return result, nil
		}
		result, err := s.applyPresetMCP(ctx, current, owner, interaction.MCPRequest{Action: "disable", Key: desktopServiceKey}, result)
		if err == nil {
			result.Output = "Computer Use disabled; the Driver installation and OS grants were kept"
		}
		return result, err
	}
	ui := request.Auth
	if ui == nil || ui.Notify == nil || ui.Input == nil {
		return result, errors.New("app: Computer Use setup requires the Settings confirmation dialog")
	}
	if s.desktop == nil {
		return result, errors.New("app: Computer Use setup is unavailable in this session")
	}
	instructions := fmt.Sprintf(desktopDisclosure, deps.CuaDriverVersion)
	if s.desktop.grant != nil {
		instructions += " It then opens CuaDriver's macOS permission flow for Accessibility and Screen Recording; only you can approve it, and cancelling cannot undo completed grants."
	}
	if s.desktop.installOptions.NoInstall {
		instructions += " Helper downloads are disabled for this instance; a compatible installed Driver can be reused."
	}
	if err := ui.Notify(ctx, interaction.AuthPrompt{Title: "Set up Computer Use", Instructions: instructions, Menu: &interaction.CommandMenu{Title: "Continue?", Options: []interaction.CommandOption{
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
	notify := func(text string) error {
		return ui.Notify(ctx, interaction.AuthPrompt{Title: "Computer Use setup", Instructions: text})
	}
	if err := notify("Verifying the pinned Cua Driver installation…"); err != nil {
		return result, err
	}
	lastMiB := int64(-1)
	installed, err := s.desktop.install(ctx, s.desktop.installOptions.WithProgress(func(p deps.Progress) error {
		mib := p.Downloaded / (1 << 20)
		if mib == lastMiB && p.Downloaded != p.Total {
			return ctx.Err()
		}
		lastMiB = mib
		return notify(fmt.Sprintf("Downloading Cua Driver %s: %.1f MiB received", p.Version, float64(p.Downloaded)/(1<<20)))
	}))
	result.Warnings = append(result.Warnings, installed.Warnings...)
	if installed.Installed || installed.Reused {
		detail := "Verified existing Driver"
		if installed.Installed {
			detail = "Installed and verified Driver"
		}
		result.External = append(result.External, interaction.SettingsActionStep{Name: "Driver installation", Detail: detail, Completed: true})
	}
	if err != nil {
		return result, err
	}
	if installed.Installation.Binary == "" {
		return result, errors.New("app: verified Driver installation is missing its binary")
	}
	if s.desktop.grant != nil {
		if err := notify("Opening CuaDriver's permission flow. Approve CuaDriver in macOS, then return here. Esc cancels this wait; system dialogs may remain open."); err != nil {
			return result, err
		}
		err := s.desktop.grant(ctx, installed.Installation.Binary)
		result.External = append(result.External, interaction.SettingsActionStep{Name: "OS permissions and live capture check", Detail: "Completed grants are not rolled back on cancellation", Completed: err == nil})
		if err != nil {
			return result, err
		}
	}
	result, err = s.ensurePresetService(ctx, desktop.ServerID, desktop.Definition(installed.Installation.Binary), nil, result)
	if err != nil {
		return result, err
	}
	result.ReadinessKnown, result.Ready = true, true
	result.Output = "Computer Use is available to the next Run through the MCP service " + desktopServiceKey
	if runtime.GOOS != "darwin" {
		result.Output += ". If its tools report missing desktop access, run `cua-driver doctor`."
	}
	return result, nil
}

// desktopSettingField is a configuration read; it performs no native or MCP I/O.
func (s *interactiveSession) desktopSettingField(configuration config.Config, owner *mcpOwner, reason string) interaction.SettingField {
	if s.desktop == nil {
		reason = "Computer Use setup is unavailable in this session"
	}
	status, tone, configured := presetStatus(configuration, owner, desktopServiceKey)
	details := []interaction.Detail{{Key: "Service", Value: desktopServiceKey + " · " + status, Tone: tone}}
	menu := &interaction.CommandMenu{Title: "Computer Use", Options: []interaction.CommandOption{{Label: "Set up", Arguments: "setup", Description: "Install the Driver, request OS access and add the " + desktopServiceKey + " MCP service"}}}
	note := "Set up installs the Cua Driver and adds the MCP service " + desktopServiceKey + "."
	if configured {
		server := configuration.MCP.Servers[desktopServiceKey]
		details = append(details, interaction.Detail{Key: "Command", Value: strings.Join(append([]string{server.Settings.Command}, server.Settings.Args...), " "), Tone: interaction.ToneReference})
		menu.Options = []interaction.CommandOption{
			{Label: "Set up / Repair", Arguments: "setup", Description: "Verify the Driver and OS access, then enable the service"},
			{Label: "Disable", Arguments: "disable", Description: "Installation and OS grants stay"},
		}
		note = "Cua owns permission mode, input delivery and sessions; AICE still asks before each tool unless you grant it."
	}
	details = append(details, interaction.Detail{Key: "Driver", Value: "Cua Driver " + deps.CuaDriverVersion + " (pinned)"}, interaction.Detail{}, interaction.Detail{Text: note, Tone: interaction.ToneMuted})
	return interaction.SettingField{ID: "desktop.setup", Category: "tools", Label: "Computer Use", Description: "Control desktop applications through the Cua Driver, exposed as the ordinary MCP service " + desktopServiceKey + ".", Keywords: []string{"desktop", "computer", "cua"}, Kind: interaction.SettingAction, Value: interaction.SettingValue{Text: status}, Tone: tone, Details: details, Action: &interaction.Command{Name: "desktop", Interactive: true, Menu: menu}, Applies: interaction.SettingDomainAction, DisabledReason: reason}
}
