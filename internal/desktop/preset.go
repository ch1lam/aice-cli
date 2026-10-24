// Package desktop is the Computer Use preset. It turns a verified Cua Driver
// installation into an ordinary MCP server definition and runs Cua's public
// OS-permission flow. Cua owns its runtime, sessions, permission mode, input
// delivery and concurrency; AICE applies its ordinary MCP boundaries.
package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

// ServerID is the user MCP service the preset writes.
const ServerID = "cua"

// Definition launches `cua-driver mcp`, the registration Cua documents for MCP
// hosts. On macOS it proxies to the signed CuaDriver.app daemon; elsewhere it
// owns its runtime in Cua's default standard mode. AICE pins the Driver, so
// telemetry and update checks stay off for the processes it starts.
func Definition(binary string) config.MCPServerSettings {
	telemetry, update := "false", "false"
	return config.MCPServerSettings{
		Name: "Computer Use (Cua Driver)", Transport: "stdio", Command: binary, Args: []string{"mcp"},
		Env: map[string]config.MCPValueRef{
			"CUA_DRIVER_RS_TELEMETRY_ENABLED": {Value: &telemetry},
			"CUA_DRIVER_RS_UPDATE_CHECK":      {Value: &update},
		},
	}
}

// GrantAvailable reports whether this platform has an OS grant flow to run.
func GrantAvailable() bool { return runtime.GOOS == "darwin" }

// Grant runs `cua-driver permissions grant`, which launches CuaDriver through
// LaunchServices so macOS attributes Accessibility and Screen Recording to the
// app, then verifies live capture. Cancellation stops waiting but cannot undo
// grants or close system dialogs. It never resets permissions.
func Grant(ctx context.Context, binary string) error {
	if !GrantAvailable() {
		return errors.New("desktop: no OS permission flow on this platform")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "permissions", "grant")
	command.Env = append(os.Environ(), "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "CUA_DRIVER_RS_UPDATE_CHECK=false")
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("desktop: Cua permission flow did not complete; approve CuaDriver in System Settings and retry setup")
	}
	return nil
}
