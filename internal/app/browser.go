package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const browserPrerequisites = `Connect to a browser with remote debugging enabled. The port is accessible to other local processes.
Chrome 144+ can enable chrome://inspect/#remote-debugging and may ask you to Allow the connection.
For a separate profile:
macOS: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Linux: google-chrome --remote-debugging-port=9222 --user-data-dir=/tmp/aice-debug
Chrome 136+ requires a non-default user-data-dir when using that flag; this new profile does not carry your existing login.`

func browserMenu() *interaction.CommandMenu {
	return &interaction.CommandMenu{Title: "Browser", Options: []interaction.CommandOption{
		{Label: "Status", Arguments: "status"},
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
	err := manager.Close(context.WithoutCancel(ctx))
	return errors.Join(err, applyBrowserEnvironment(manager))
}

func (s *interactiveSession) slashBrowser(ctx context.Context, request interaction.CommandRequest) (string, error) {
	if runtime.GOOS == "windows" {
		return "browser automation is not supported on Windows in this version", nil
	}
	if s.browser == nil {
		return "", fmt.Errorf("browser automation is unavailable: session setup failed")
	}
	action := strings.TrimSpace(request.Arguments)
	if action == "" || action == "status" {
		return s.browserStatus(ctx)
	}
	s.conversation.historyMu.RLock()
	active := s.conversation.activeMainRun != nil
	s.conversation.historyMu.RUnlock()
	if active {
		return "", fmt.Errorf("app: cannot change the browser while a response is running")
	}
	if action == "close" {
		err := closeBrowser(ctx, s.browser)
		// close may acknowledge before the daemon exits; never reuse that name.
		rotateErr := s.browser.Rotate()
		err = errors.Join(err, rotateErr, applyBrowserEnvironment(s.browser))
		return "Browser session closed", err
	}
	if _, err := s.browser.Executable(); err != nil {
		return "", err
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return "", fmt.Errorf("browser connection and tab selection require the interactive menu")
	}
	switch action {
	case "auto", "connect":
		prompt := interaction.AuthPrompt{Title: "Connect browser", Instructions: browserPrerequisites, AllowInput: true, InputLabel: "CDP port or ws:// URL"}
		target := browser.Target{Auto: action == "auto"}
		if action == "auto" {
			prompt.AllowInput = false
			prompt.Menu = &interaction.CommandMenu{Title: "Connect", Options: []interaction.CommandOption{{Label: "Connect (auto-detect)", Arguments: "connect"}}}
		}
		value, err := browserPrompt(ctx, request.Auth, prompt)
		if err != nil {
			return "", err
		}
		if action == "connect" {
			target.Endpoint = strings.TrimSpace(value)
		}
		tabs, connectErr := s.browser.Connect(ctx, target)
		// Publish even failure state: a partial connect must not silently leave the
		// next model command pointing at the previous browser.
		envErr := applyBrowserEnvironment(s.browser)
		if err := errors.Join(connectErr, envErr); err != nil {
			return "", err
		}
		return s.chooseBrowserTab(ctx, request.Auth, tabs, true)
	case "tabs":
		tabs, err := s.browser.Tabs(ctx)
		if err != nil {
			return "", err
		}
		return s.chooseBrowserTab(ctx, request.Auth, tabs, false)
	default:
		return "", fmt.Errorf("unknown browser action %q", action)
	}
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
func (s *interactiveSession) chooseBrowserTab(ctx context.Context, ui *interaction.AuthInteraction, tabs []browser.Tab, connected bool) (string, error) {
	menu := &interaction.CommandMenu{Title: "Choose browser tab", Options: []interaction.CommandOption{{Label: "New tab (default)", Arguments: "new"}}}
	for _, tab := range tabs {
		menu.Options = append(menu.Options, interaction.CommandOption{Label: tab.Title + " — " + tab.URL, Arguments: tab.TargetID})
	}
	choice, err := browserPrompt(ctx, ui, interaction.AuthPrompt{Title: menu.Title, Instructions: "Use ↑/↓ and Enter. Login and cookies are shared with other tabs in this browser.", Menu: menu})
	if err != nil {
		return "", err
	}
	if choice == "new" {
		if !connected {
			if err := s.browser.NewTab(ctx); err != nil {
				return "", err
			}
		}
		return "Browser connected to a new tab. Observe it with agent-browser snapshot -i.", nil
	}
	for _, tab := range tabs {
		if choice == tab.TargetID {
			if err := s.browser.BindTab(ctx, choice); err != nil {
				return "", err
			}
			return "Browser bound to: " + tab.Title + " — " + tab.URL, nil
		}
	}
	return "", fmt.Errorf("selected browser tab is no longer available")
}
func (s *interactiveSession) browserStatus(ctx context.Context) (string, error) {
	executable, err := s.browser.Executable()
	lines := []string{"Browser helper: " + executable, "Pinned version: " + deps.AgentBrowserVersion, "Session: " + s.browser.Name(), "Run directory: " + s.browser.RunDir(), fmt.Sprintf("Sidecar present: %v", s.browser.HasSidecar())}
	if os.Getenv("AICE_NO_DEP_INSTALL") != "" {
		lines = append(lines, "Automatic installation disabled (AICE_NO_DEP_INSTALL)")
	}
	if err != nil {
		return strings.Join(append(lines, err.Error()), "\n"), nil
	}
	info, err := s.browser.Info(ctx)
	if err != nil {
		return strings.Join(lines, "\n"), err
	}
	mode := "managed headless (on first browser command)"
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
