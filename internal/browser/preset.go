// Package browser is the browser automation preset. It turns the pinned
// agent-browser helper into an ordinary MCP server definition. agent-browser
// owns its sessions, daemon lifetime, Chrome processes and tools; AICE applies
// its ordinary MCP boundaries.
package browser

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

const (
	// ServerID is the user MCP service the preset writes.
	ServerID = "browser"
	// Session keeps AICE's browser apart from the user's default agent-browser session.
	Session = "aice"
	// idleTimeoutMS lets agent-browser stop an unused daemon and its Chrome.
	idleTimeoutMS = "600000"
)

// Target selects an existing browser over CDP. The zero value is a separate
// browser that agent-browser launches with a temporary profile.
type Target struct {
	Endpoint string // CDP port or ws:// / wss:// URL
	Auto     bool   // auto-detect a running Chrome
}

// Validate accepts the zero target, auto-detect, a port or a WebSocket URL.
func (t Target) Validate() error {
	if t.Endpoint == "" {
		return nil
	}
	if t.Auto {
		return fmt.Errorf("choose auto-detect or a CDP endpoint, not both")
	}
	if port, err := strconv.Atoi(t.Endpoint); err == nil && port > 0 && port <= 65535 {
		return nil
	}
	u, err := url.Parse(t.Endpoint)
	if err == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(t.Endpoint, " \t\r\n") {
		return nil
	}
	return fmt.Errorf("invalid CDP endpoint: expected port 1–65535 or ws:// / wss:// URL")
}

// OwnedEnv lists every env key the preset sets; other entries in the service,
// such as AGENT_BROWSER_EXECUTABLE_PATH, belong to the user.
var OwnedEnv = []string{"AGENT_BROWSER_SESSION", "AGENT_BROWSER_HEADED", "AGENT_BROWSER_IDLE_TIMEOUT_MS", "AGENT_BROWSER_AUTO_CONNECT", "AGENT_BROWSER_CDP"}

// Definition launches `agent-browser mcp` with its default core tool profile.
func Definition(binary string, headed bool, target Target) config.MCPServerSettings {
	env := map[string]config.MCPValueRef{
		"AGENT_BROWSER_SESSION":         literal(Session),
		"AGENT_BROWSER_HEADED":          literal(strconv.FormatBool(headed)),
		"AGENT_BROWSER_IDLE_TIMEOUT_MS": literal(idleTimeoutMS),
	}
	switch {
	case target.Auto:
		env["AGENT_BROWSER_AUTO_CONNECT"] = literal("true")
	case target.Endpoint != "":
		env["AGENT_BROWSER_CDP"] = literal(target.Endpoint)
	}
	return config.MCPServerSettings{Name: "Browser (agent-browser)", Transport: "stdio", Command: binary, Args: []string{"mcp"}, Env: env}
}

// Options reads the window and target choices back from a preset definition.
func Options(settings config.MCPServerSettings) (headed bool, target Target) {
	value := func(name string) string {
		if ref, ok := settings.Env[name]; ok && ref.Value != nil {
			return *ref.Value
		}
		return ""
	}
	return value("AGENT_BROWSER_HEADED") == "true", Target{Endpoint: value("AGENT_BROWSER_CDP"), Auto: value("AGENT_BROWSER_AUTO_CONNECT") == "true"}
}

// Close asks agent-browser to close the preset session: a launched browser
// exits, a connected one is only disconnected.
func Close(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--session", Session, "close")
	command.Env = os.Environ()
	command.WaitDelay = 2 * time.Second
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("browser: close session: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func literal(value string) config.MCPValueRef { return config.MCPValueRef{Value: &value} }
