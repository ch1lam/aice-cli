package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const browserPrerequisites = `Connect to a browser with remote debugging enabled. The port is accessible to other local processes.
Chrome 144+ can enable chrome://inspect/#remote-debugging and may ask you to Allow the connection.
For a separate profile:
macOS: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Linux: google-chrome --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Chrome 136+ requires a non-default user-data-dir when using that flag; this new profile does not carry your existing login.`

func (s *interactiveSession) browserMenu() *interaction.CommandMenu {
	visibility := "off"
	if s.settingsSnapshot().configuration.BrowserHeaded {
		visibility = "on"
	}
	return &interaction.CommandMenu{Title: "Browser", Options: []interaction.CommandOption{
		{Label: "Status", Arguments: "status"},
		{Label: "Show window: " + visibility + " (toggle)", Description: "Save visibility for the next managed browser session", Arguments: "headed"},
		{Label: "Connect to running browser (auto-detect)", Arguments: "auto"},
		{Label: "Connect to port or URL…", Arguments: "connect"},
		{Label: "Choose tab…", Arguments: "tabs"},
		{Label: "Close", Arguments: "close"},
	}}
}

func applyBrowserEnvironment(manager *browser.Manager) error {
	if manager == nil {
		return nil
	}
	var errs []error
	for key, value := range manager.Environment() {
		var err error
		if value == "" {
			err = os.Unsetenv(key)
		} else {
			err = os.Setenv(key, value)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("set browser environment %s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}
func closeBrowser(ctx context.Context, manager *browser.Manager) error {
	if manager == nil {
		return nil
	}
	// Escape and shutdown cancel the parent. Cleanup still gets its own bounded
	// opportunity to disconnect; Manager.Close owns the ten-second deadline.
	_, err := manager.Close(context.WithoutCancel(ctx))
	return errors.Join(err, applyBrowserEnvironment(manager))
}

type browserActionResult struct {
	output           string
	resourcesChanged bool // Changed locally, or a started modifying helper may have changed them.
}

// runBrowserSettings gives both frontends one reservation and completion rule.
// Slash status remains available during a response; Settings actions retain
// their revision check and idle requirement, including status.
func (s *interactiveSession) runBrowserSettings(ctx context.Context, revision *uint64, action string, ui *interaction.AuthInteraction) (interaction.SettingsActionResult, error) {
	action = strings.TrimSpace(action)
	if revision == nil && (action == "" || action == "status") {
		result, err := s.runBrowserAction(ctx, action, ui)
		return interaction.SettingsActionResult{Output: result.output}, err
	}
	if err := s.beginSettingsOperation(revision, true); err != nil {
		return interaction.SettingsActionResult{}, err
	}
	result, err := s.runBrowserAction(ctx, action, ui)
	nextRevision, warnings := s.endSettingsOperation(result.resourcesChanged, result.resourcesChanged)
	return interaction.SettingsActionResult{Output: result.output, Revision: nextRevision, Warnings: warnings}, err
}

func (s *interactiveSession) runBrowserAction(ctx context.Context, action string, ui *interaction.AuthInteraction) (browserActionResult, error) {
	result := browserActionResult{}
	if runtime.GOOS == "windows" {
		result.output = "browser automation is not supported on Windows in this version"
		return result, nil
	}
	if s.browser == nil {
		return result, fmt.Errorf("browser automation is unavailable: session setup failed")
	}
	action = strings.TrimSpace(action)
	if action == "" || action == "status" {
		var err error
		result.output, err = s.browserStatus(ctx)
		return result, err
	}
	s.conversation.historyMu.RLock()
	active := s.conversation.activeMainRun != nil
	s.conversation.historyMu.RUnlock()
	if active {
		return result, fmt.Errorf("app: cannot change the browser while a response is running")
	}
	if action == "headed" {
		return s.toggleBrowserWindow(ctx)
	}
	if action == "close" {
		changed, err := s.browser.Close(context.WithoutCancel(ctx))
		err = errors.Join(err, applyBrowserEnvironment(s.browser))
		// close may acknowledge before the daemon exits; never reuse that name.
		rotateErr := s.browser.Rotate()
		result.resourcesChanged = changed || rotateErr == nil
		result.output = "Browser session closed"
		err = errors.Join(err, rotateErr, applyBrowserEnvironment(s.browser))
		return result, err
	}
	if _, err := s.browser.Executable(); err != nil {
		return result, err
	}
	if ui == nil || ui.Notify == nil {
		return result, fmt.Errorf("browser connection and tab selection require the interactive menu")
	}
	switch action {
	case "auto", "connect":
		prompt := interaction.AuthPrompt{Title: "Connect browser", Instructions: browserPrerequisites, AllowInput: true, InputLabel: "CDP port or ws:// URL"}
		target := browser.Target{Auto: action == "auto"}
		if action == "auto" {
			prompt.AllowInput = false
			prompt.Menu = &interaction.CommandMenu{Title: "Connect", Options: []interaction.CommandOption{{Label: "Connect (auto-detect)", Arguments: "connect"}}}
		}
		value, err := browserPrompt(ctx, ui, prompt)
		if err != nil {
			return result, err
		}
		if action == "connect" {
			target.Endpoint = strings.TrimSpace(value)
		}
		tabs, changed, connectErr := s.browser.Connect(ctx, target)
		result.resourcesChanged = changed
		// Publish even failure state: a partial connect must not silently leave the
		// next model command pointing at the previous browser.
		envErr := applyBrowserEnvironment(s.browser)
		if err := errors.Join(connectErr, envErr); err != nil {
			return result, err
		}
		selection, err := s.chooseBrowserTab(ctx, ui, tabs, true)
		selection.resourcesChanged = selection.resourcesChanged || result.resourcesChanged
		return selection, err
	case "tabs":
		tabs, err := s.browser.Tabs(ctx)
		if err != nil {
			return result, err
		}
		return s.chooseBrowserTab(ctx, ui, tabs, false)
	default:
		return result, fmt.Errorf("unknown browser action %q", action)
	}
}
func (s *interactiveSession) toggleBrowserWindow(ctx context.Context) (browserActionResult, error) {
	result := browserActionResult{}
	current := s.settingsSnapshot().configuration
	headed := !current.BrowserHeaded
	changes := map[config.Setting]string{config.SettingBrowserHeaded: strconv.FormatBool(headed)}
	configuration, err := s.persistSettings(ctx, current, changes)
	if err != nil {
		return result, err
	}
	s.stateMu.Lock()
	s.configuration = configuration
	s.stateMu.Unlock()
	s.browser.SetHeaded(headed)
	result.resourcesChanged = true
	if err := applyBrowserEnvironment(s.browser); err != nil {
		return result, fmt.Errorf("browser window preference saved, but environment update failed: %w", err)
	}
	state := "off"
	if headed {
		state = "on"
	}
	output := "Show window: " + state + " (saved)"
	if s.browser.Target() != (browser.Target{}) {
		output += "\nApplies to AICE-managed browsers; the connected browser is unchanged."
	} else if s.browser.Headed() != headed {
		output += "\nThe current browser is unchanged. Use /browser → Close, then open a page to apply; closing discards its temporary pages and login state."
	} else {
		output += "\nApplies when AICE next opens a browser."
	}
	result.output = output + savedOverrideNotice(configuration, changes)
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
func (s *interactiveSession) chooseBrowserTab(ctx context.Context, ui *interaction.AuthInteraction, tabs []browser.Tab, connected bool) (browserActionResult, error) {
	result := browserActionResult{}
	menu := &interaction.CommandMenu{Title: "Choose browser tab", Options: []interaction.CommandOption{{Label: "New tab (default)", Arguments: "new"}}}
	for _, tab := range tabs {
		menu.Options = append(menu.Options, interaction.CommandOption{Label: tab.Title + " — " + tab.URL, Arguments: tab.TargetID})
	}
	choice, err := browserPrompt(ctx, ui, interaction.AuthPrompt{Title: menu.Title, Instructions: "Use ↑/↓ and Enter. Login and cookies are shared with other tabs in this browser.", Menu: menu})
	if err != nil {
		return result, err
	}
	if choice == "new" {
		if !connected {
			var err error
			result.resourcesChanged, err = s.browser.NewTab(ctx)
			if err != nil {
				return result, err
			}
		}
		result.output = "Browser connected to a new tab. Observe it with agent-browser snapshot -i."
		return result, nil
	}
	for _, tab := range tabs {
		if choice == tab.TargetID {
			var err error
			result.resourcesChanged, err = s.browser.BindTab(ctx, choice)
			if err != nil {
				return result, err
			}
			result.output = "Browser bound to: " + tab.Title + " — " + tab.URL
			return result, nil
		}
	}
	return result, fmt.Errorf("selected browser tab is no longer available")
}
func (s *interactiveSession) browserStatus(ctx context.Context) (string, error) {
	executable, err := s.browser.Executable()
	lines := []string{"Browser helper: " + executable, "Pinned version: " + deps.AgentBrowserVersion, "Session: " + s.browser.Name(), "Run directory: " + s.browser.RunDir(), fmt.Sprintf("Sidecar present: %v", s.browser.HasSidecar())}
	headed := s.settingsSnapshot().configuration.BrowserHeaded
	visibility := "off"
	if headed {
		visibility = "on"
	}
	lines = append(lines, "Show window: "+visibility+" (preference)")
	if s.browser.Target() == (browser.Target{}) && s.browser.Headed() != headed {
		lines = append(lines, "Window preference pending: close this browser session to apply on next open")
	}
	if s.settingsSnapshot().configuration.NoDepInstall {
		lines = append(lines, "Automatic installation disabled (no_dep_install)")
	}
	if err != nil {
		return strings.Join(append(lines, err.Error()), "\n"), nil
	}
	info, err := s.browser.Info(ctx)
	if err != nil {
		return strings.Join(lines, "\n"), err
	}
	mode := "managed headless (on first browser command)"
	if s.browser.Headed() {
		mode = "managed headed (show window)"
	}
	target := s.browser.Target()
	if target.Auto {
		mode = "external, auto-detect"
	} else if target.Endpoint != "" {
		mode = "external CDP"
	}
	lines = append(lines, "Mode: "+mode, fmt.Sprintf("Daemon active: %v; browser connected: %v; tabs: %d", info.Active, info.Runtime.BrowserLaunched, info.Runtime.PageCount))
	if info.Active && info.Runtime.BrowserLaunched {
		tabs, err := s.browser.Tabs(ctx)
		if err != nil {
			lines = append(lines, "Connection unavailable: "+err.Error())
		} else {
			for _, tab := range tabs {
				if tab.Active {
					lines = append(lines, "Bound tab: "+tab.ID+" — "+tab.Title+" — "+tab.URL)
				}
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
