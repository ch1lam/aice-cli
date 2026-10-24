package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const browserServiceKey = "user:" + browser.ServerID

const browserPrerequisites = `Connect to a browser with remote debugging enabled. The port is accessible to other local processes.
Chrome 144+ can enable chrome://inspect/#remote-debugging and may ask you to Allow the connection.
For a separate profile:
macOS: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Linux: google-chrome --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Chrome 136+ requires a non-default user-data-dir when using that flag; this new profile does not carry your existing login.`

// browserHelper is the pinned agent-browser installation, or "" when it is
// unavailable on this platform or not yet provisioned.
func (a *application) browserHelper() string {
	home, err := a.userHome()
	if err != nil || home == "" || runtime.GOOS == "windows" {
		return ""
	}
	binDir := filepath.Join(home, ".aice", "bin")
	if !deps.AgentBrowserInstalled(binDir) {
		return ""
	}
	return filepath.Join(binDir, "agent-browser")
}

// browserChoices reads the window and target from the current service.
func browserChoices(configuration config.Config) (configured bool, headed bool, target browser.Target) {
	server, exists := configuration.MCP.Servers[browserServiceKey]
	if !exists {
		return false, false, browser.Target{}
	}
	headed, target = browser.Options(server.Settings)
	return true, headed, target
}

func (s *interactiveSession) browserMenu() *interaction.CommandMenu {
	_, headed, _ := browserChoices(s.settingsSnapshot().configuration)
	visibility := "off"
	if headed {
		visibility = "on"
	}
	return &interaction.CommandMenu{Title: "Browser", Options: []interaction.CommandOption{
		{Label: "Set up / Repair", Arguments: "setup", Description: "Add the user:browser MCP service for the pinned agent-browser"},
		{Label: "Show window: " + visibility + " (toggle)", Arguments: "headed", Description: "Applies when agent-browser next launches a browser"},
		{Label: "Connect to running browser (auto-detect)", Arguments: "auto"},
		{Label: "Connect to port or URL…", Arguments: "connect"},
		{Label: "Use a separate browser", Arguments: "separate", Description: "Launch a browser with a temporary profile"},
		{Label: "Close browser session", Arguments: "close", Description: "Close the launched browser or disconnect the connected one"},
		{Label: "Disable", Arguments: "disable", Description: "Disable the user:browser MCP service"},
	}}
}

// runBrowserSettings shares the Settings reservation: service changes replace
// MCP resources only while idle. Closing the session leaves configuration as is.
func (s *interactiveSession) runBrowserSettings(ctx context.Context, revision *uint64, action string, ui *interaction.AuthInteraction) (result interaction.SettingsActionResult, returnErr error) {
	action = strings.TrimSpace(action)
	if action == "" || action == "status" {
		settings := s.settingsSnapshot()
		result.Output = interaction.DetailsText(s.browserDetails(settings.configuration, settings.mcp))
		return result, nil
	}
	if s.browserBin == "" {
		return result, fmt.Errorf("browser automation is unavailable: the pinned agent-browser helper is not installed on this platform")
	}
	if err := s.beginSettingsOperation(revision, true); err != nil {
		return result, err
	}
	defer func() {
		var warnings []string
		changed := result.Committed
		result.Revision, warnings = s.endSettingsOperation(changed, changed)
		result.Warnings = append(result.Warnings, warnings...)
	}()
	configuration := s.settingsSnapshot().configuration
	configured, headed, target := browserChoices(configuration)
	switch action {
	case "close":
		if err := browser.Close(ctx, s.browserBin); err != nil {
			return result, err
		}
		result.Output = "Browser session closed"
		return result, nil
	case "disable":
		if !configured {
			result.Output = "Browser automation is not configured"
			return result, nil
		}
		result, err := s.applyPresetMCP(ctx, configuration, s.settingsSnapshot().mcp, interaction.MCPRequest{Action: "disable", Key: browserServiceKey}, result)
		if err == nil {
			result.Output = "Browser automation disabled; the helper stays installed"
		}
		return result, err
	case "setup":
	case "headed":
		headed = !headed
	case "separate":
		target = browser.Target{}
	case "auto", "connect":
		if ui == nil || ui.Notify == nil || ui.Input == nil {
			return result, fmt.Errorf("browser connection requires the interactive menu")
		}
		prompt := interaction.AuthPrompt{Title: "Connect browser", Instructions: browserPrerequisites, AllowInput: true, InputLabel: "CDP port or ws:// URL"}
		if action == "auto" {
			prompt.AllowInput = false
			prompt.Menu = &interaction.CommandMenu{Title: "Connect", Options: []interaction.CommandOption{{Label: "Connect (auto-detect)", Arguments: "connect"}}}
		}
		value, err := browserPrompt(ctx, ui, prompt)
		if err != nil {
			return result, err
		}
		target = browser.Target{Auto: action == "auto"}
		if action == "connect" {
			target.Endpoint = strings.TrimSpace(value)
		}
	default:
		return result, fmt.Errorf("unknown browser action %q", action)
	}
	if err := target.Validate(); err != nil {
		return result, err
	}
	result, err := s.ensurePresetService(ctx, browser.ServerID, browser.Definition(s.browserBin, headed, target), browser.OwnedEnv, result)
	if err != nil {
		return result, err
	}
	result.Output = "Browser automation is available to the next Run through the MCP service " + browserServiceKey
	if action != "setup" && configured {
		result.Output += ". An already running browser session keeps its window and connection until it is closed."
	}
	return result, nil
}

func browserPrompt(ctx context.Context, ui *interaction.AuthInteraction, prompt interaction.AuthPrompt) (string, error) {
	if err := ui.Notify(ctx, prompt); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value, ok := <-ui.Input:
		if !ok {
			return "", fmt.Errorf("browser input closed")
		}
		return value, nil
	}
}

// browserDetails is a configuration read; it performs no helper or MCP I/O.
func (s *interactiveSession) browserDetails(configuration config.Config, owner *mcpOwner) []interaction.Detail {
	helper, helperTone := s.browserBin, interaction.ToneReference
	if helper == "" {
		helper, helperTone = "not installed", interaction.ToneWarning
	}
	status, tone, configured := presetStatus(configuration, owner, browserServiceKey)
	details := []interaction.Detail{
		{Key: "Service", Value: browserServiceKey + " · " + status, Tone: tone},
		{Key: "Helper", Value: helper, Tone: helperTone},
		{Key: "Version", Value: "agent-browser " + deps.AgentBrowserVersion},
	}
	if !configured {
		return append(details, interaction.Detail{}, interaction.Detail{Text: "Set up adds the MCP service " + browserServiceKey + ".", Tone: interaction.ToneMuted})
	}
	_, headed, target := browserChoices(configuration)
	window := "hidden"
	if headed {
		window = "shown"
	}
	return append(details,
		interaction.Detail{Key: "Session", Value: browser.Session + " · " + browserTargetText(target) + " · window " + window},
		interaction.Detail{},
		interaction.Detail{Text: "agent-browser owns the session and stops an idle browser after 10 minutes.", Tone: interaction.ToneMuted})
}

func browserTargetText(target browser.Target) string {
	switch {
	case target.Auto:
		return "Running browser · auto-detect"
	case target.Endpoint != "":
		return "Running browser · " + target.Endpoint
	}
	return "Separate browser"
}

// browserSettingFields shows the service row, and once it exists, the
// window and connection choices as direct rows instead of a nested menu.
func (s *interactiveSession) browserSettingFields(configuration config.Config, owner *mcpOwner, reason string) []interaction.SettingField {
	if s.browserBin == "" {
		reason = "The pinned agent-browser helper is not installed on this platform"
	}
	status, tone, configured := presetStatus(configuration, owner, browserServiceKey)
	field := func(id, label, description, value string, command *interaction.Command) interaction.SettingField {
		return interaction.SettingField{ID: id, Category: "tools", Label: label, Description: description, Keywords: []string{"browser", "agent-browser", "chrome"}, Kind: interaction.SettingAction, Value: interaction.SettingValue{Text: value}, Applies: interaction.SettingDomainAction, DisabledReason: reason, Action: command}
	}
	menu := &interaction.CommandMenu{Title: "Browser automation", Options: []interaction.CommandOption{{Label: "Set up", Arguments: "setup", Description: "Add the " + browserServiceKey + " MCP service for the pinned agent-browser"}}}
	if configured {
		menu.Options = []interaction.CommandOption{
			{Label: "Set up / Repair", Arguments: "setup", Description: "Rewrite and enable the service for the pinned helper"},
			{Label: "Close browser session", Arguments: "close", Description: "Close the launched browser or disconnect the connected one"},
			{Label: "Disable", Arguments: "disable", Description: "Keep the helper installed"},
		}
	}
	service := field("browser.service", "Browser automation", "Browser automation through agent-browser, exposed as the ordinary MCP service "+browserServiceKey+".", status, &interaction.Command{Name: "browser", Interactive: true, Menu: menu})
	service.Tone, service.Details = tone, s.browserDetails(configuration, owner)
	if !configured {
		return []interaction.SettingField{service}
	}
	_, headed, target := browserChoices(configuration)
	window := field("browser.window", "Show window", "Show the browser window; applies when agent-browser next launches a browser.", onOff(headed), &interaction.Command{Name: "browser", Interactive: true})
	window.Arguments = "headed"
	connection := field("browser.connection", "Connect to", "Launch a separate browser, or connect to a running one with remote debugging enabled.", browserTargetText(target), &interaction.Command{Name: "browser", Interactive: true, Menu: &interaction.CommandMenu{Title: "Connect to", Options: []interaction.CommandOption{
		{Label: "Separate browser", Arguments: "separate", Description: "Launch a browser with a temporary profile"},
		{Label: "Running browser · auto-detect", Arguments: "auto"},
		{Label: "Running browser · port or URL…", Arguments: "connect"},
	}}})
	return []interaction.SettingField{service, window, connection}
}
