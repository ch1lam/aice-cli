package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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

func desktopCommand() *interaction.Command {
	return &interaction.Command{Name: "desktop", Interactive: true, Menu: &interaction.CommandMenu{Title: "Computer Use", Options: []interaction.CommandOption{
		{Label: "Set up / Repair", Arguments: "setup", Description: "Install the Driver, request OS access and add the user:cua MCP service"},
		{Label: "Disable", Arguments: "disable", Description: "Disable the user:cua MCP service; installation and OS grants stay"},
	}}}
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
		return s.applyDesktopMCP(ctx, current, owner, interaction.MCPRequest{Action: "disable", Key: desktopServiceKey}, result)
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
	definition := desktop.Definition(installed.Installation.Binary)
	encoded, err := json.Marshal(definition)
	if err != nil {
		return result, err
	}
	op := interaction.MCPRequest{Action: "add", Key: desktop.ServerID, Definition: encoded}
	if existing, exists := current.MCP.Servers[desktopServiceKey]; exists {
		if existing.Source.Kind != "user" {
			return result, errors.New("app: a project MCP service already uses the cua ID; remove it before setup")
		}
		op = interaction.MCPRequest{Action: "replace", Key: desktopServiceKey, Definition: encoded}
		if reflect.DeepEqual(desktopComparable(existing.Settings), desktopComparable(definition)) {
			op = interaction.MCPRequest{Action: "enable", Key: desktopServiceKey}
		}
	}
	result, err = s.applyDesktopMCP(ctx, current, owner, op, result)
	if err != nil {
		return result, err
	}
	// The user just reviewed this exact service; approve its connection so the
	// next Run can discover it. Tool calls still ask through Guard.
	s.stateMu.RLock()
	current, owner = s.configuration, s.mcp
	s.stateMu.RUnlock()
	server := current.MCP.Servers[desktopServiceKey]
	if current.MCP.ConnectionDecision(desktopServiceKey) != config.MCPConnectionAllow {
		result, err = s.applyDesktopMCP(ctx, current, owner, interaction.MCPRequest{Action: "approve", Key: desktopServiceKey, Fingerprint: server.Fingerprint}, result)
		if err != nil {
			return result, err
		}
	}
	result.ReadinessKnown, result.Ready = true, true
	result.Output = "Computer Use is available to the next Run through the MCP service " + desktopServiceKey
	if runtime.GOOS != "darwin" {
		result.Output += ". If its tools report missing desktop access, run `cua-driver doctor`."
	}
	return result, nil
}

// desktopComparable ignores user preferences such as enablement and tool
// filters when deciding whether the connection definition must be replaced.
func desktopComparable(settings config.MCPServerSettings) config.MCPServerSettings {
	return config.MCPServerSettings{Transport: settings.Transport, Command: settings.Command, Args: settings.Args, Cwd: settings.Cwd, Env: settings.Env}
}

func (s *interactiveSession) applyDesktopMCP(ctx context.Context, current config.Config, owner *mcpOwner, op interaction.MCPRequest, result interaction.SettingsActionResult) (interaction.SettingsActionResult, error) {
	next, managed, err := executeMCPManagement(ctx, current, owner, op, nil)
	result.Warnings = append(result.Warnings, managed.Warnings...)
	result.Committed = result.Committed || managed.Committed
	if managed.Committed {
		applyErr := s.publishMCPConfiguration(next)
		result.Applied = applyErr == nil
		err = errors.Join(err, applyErr)
	}
	if err == nil && op.Action == "disable" {
		result.Output = "Computer Use disabled; the Driver installation and OS grants were kept"
	}
	return result, err
}

// desktopStatus is a configuration read; it performs no native or MCP I/O.
func (s *interactiveSession) desktopStatus(configuration config.Config, owner *mcpOwner) string {
	server, exists := configuration.MCP.Servers[desktopServiceKey]
	if !exists {
		return fmt.Sprintf("Not configured. Set up installs Cua Driver %s and adds the MCP service %s.", deps.CuaDriverVersion, desktopServiceKey)
	}
	state := "disabled"
	if server.Enabled {
		state = "enabled"
		for _, status := range owner.Status() {
			if status.Key == desktopServiceKey {
				state += " · " + status.State
			}
		}
	}
	return fmt.Sprintf("MCP service %s: %s\nCommand: %s %s\nPinned Driver: %s. Cua owns permission mode, input delivery and sessions; manage the service with /mcp.", desktopServiceKey, state, server.Settings.Command, strings.Join(server.Settings.Args, " "), deps.CuaDriverVersion)
}
