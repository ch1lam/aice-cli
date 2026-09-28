package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

var errManagedCUAConnection = errors.New("CUA connection belongs to managed:cua; use Computer Use settings (/desktop), or configure an explicit independent endpoint or --direct runtime")

// The application reserves its native endpoint even when Computer Use is off.
// This is configuration admission, not isolation from arbitrary host programs.
// Neither construction nor status inspection probes or starts a native service.
func (a *application) newConfiguredMCPOwner(c config.Config, gate *guard.Guard, yolo bool) (*mcpOwner, error) {
	home, _ := a.userHome()
	check := func(wire mcpclient.Config) error {
		if wire.Stdio != nil && duplicatesManagedCUA(*wire.Stdio, home, runtime.GOOS) {
			return errManagedCUAConnection
		}
		return nil
	}
	open := a.dependencies.openMCP
	if open == nil {
		open = openMCPConnection
	}
	owner, err := newMCPOwner(c.MCP, gate, yolo, func(ctx context.Context, wire mcpclient.Config) (mcpOwnedConnection, error) {
		if err := check(wire); err != nil {
			return nil, err
		}
		return open(ctx, wire)
	}, mcpOAuthRefresh(c.Paths))
	if err == nil {
		owner.checkConnection = check
	}
	return owner, err
}

func mcpStdioConfiguration(server config.MCPServer) *mcpclient.StdioConfig {
	if server.Settings.Transport != "stdio" {
		return nil
	}
	env, _ := server.ConnectionValues()
	return &mcpclient.StdioConfig{Executable: server.Settings.Command, Args: slices.Clone(server.Settings.Args), Dir: server.Settings.Cwd, Env: env}
}

func duplicatesManagedCUA(wire mcpclient.StdioConfig, home, goos string) bool {
	endpoint := desktopServiceEndpointFor(home, goos)
	// An explicitly addressed reserved endpoint remains reserved even through
	// a differently named proxy. Match both forms accepted by Driver 0.29.1.
	for i, arg := range wire.Args {
		value, socket := strings.CutPrefix(arg, "--socket=")
		if arg == "--socket" && i+1 < len(wire.Args) {
			value, socket = wire.Args[i+1], true
		}
		if socket && sameNativeEndpoint(value, endpoint, wire.Dir, goos) {
			return true
		}
	}
	command := nativePath(wire.Executable)
	for _, part := range strings.Split(filepath.ToSlash(command), "/") {
		if part == "CuaDriverLocal.app" {
			return false // The pinned source-build product has its own namespace.
		}
	}
	name := strings.ToLower(filepath.Base(command))
	if name == "cua-driver-local" || name == "cua-driver-local.exe" {
		return false
	}
	if name != "cua-driver" && name != "cua-driver.exe" && name != "cua-driver-rs" && !sameInstalledCUABinary(command, home, goos) {
		return false
	}
	for i, arg := range wire.Args {
		if strings.HasPrefix(arg, "--socket=") || arg == "--socket" && i+1 < len(wire.Args) {
			return false // An explicit different endpoint is independently owned.
		}
	}
	if slices.Contains(wire.Args, "--direct") {
		return false
	}
	// A bare macOS client uses its HOME-derived service. Linux/Windows can
	// also choose the shared daemon when history preview is enabled. Require
	// an explicit independent runtime instead of guessing mutable preferences.
	childHome, present := wire.Env["HOME"]
	if !present && !wire.ReplaceEnvironment {
		childHome, present = os.LookupEnv("HOME")
	}
	if !present {
		childHome = "/tmp" // Pinned Driver's missing-HOME fallback.
	}
	return sameNativeEndpoint(desktopServiceEndpointFor(childHome, goos), endpoint, wire.Dir, goos)
}

func sameInstalledCUABinary(command, home, goos string) bool {
	installed := "/Applications/CuaDriver.app/Contents/MacOS/cua-driver"
	if goos != "darwin" {
		name := "cua-driver"
		if goos == "windows" {
			name += ".exe"
		}
		installed = filepath.Join(home, ".aice", "bin", "cua", deps.CuaDriverVersion, goos+"-"+runtime.GOARCH, name)
	}
	left, leftErr := os.Stat(command)
	right, rightErr := os.Stat(installed)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

func sameNativeEndpoint(candidate, managed, directory, goos string) bool {
	if candidate == "" || managed == "" {
		return false
	}
	if goos == "windows" {
		return strings.EqualFold(strings.ReplaceAll(candidate, "/", `\`), managed)
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(directory, candidate)
	}
	return nativePath(candidate) == nativePath(managed)
}

// Resolve existing aliases, including an existing parent of a not-yet-created
// socket. No socket connection, executable invocation or directory creation.
func nativePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path || parent == "." {
		return filepath.Clean(path)
	}
	return filepath.Join(nativePath(parent), filepath.Base(path))
}
