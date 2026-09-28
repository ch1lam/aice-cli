package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func TestManagedCUAConnectionIdentity(t *testing.T) {
	home := t.TempDir()
	for _, goos := range []string{"darwin", "linux", "windows"} {
		endpoint := desktopServiceEndpointFor(home, goos)
		for _, tc := range []struct {
			name string
			args []string
			want bool
		}{
			{"default", nil, true},
			{"explicit-mcp", []string{"mcp"}, true},
			{"proxy", []string{"mcp", "--socket", endpoint, "--embedded"}, true},
			{"equals", []string{"mcp", "--socket=" + endpoint}, true},
			{"duplicate-flags", []string{"mcp", "--socket", endpoint, "--socket", "other"}, true},
			{"independent", []string{"mcp", "--socket", filepath.Join(home, "independent.sock")}, false},
			{"direct", []string{"mcp", "--direct"}, false},
			{"direct-with-reserved-endpoint", []string{"mcp", "--direct", "--socket", endpoint}, true},
		} {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				wire := mcpclient.StdioConfig{Executable: filepath.Join(home, "cua-driver"), Dir: home, Args: tc.args, Env: map[string]string{"HOME": home}}
				if got := duplicatesManagedCUA(wire, home, goos); got != tc.want {
					t.Fatalf("duplicate = %t; want %t", got, tc.want)
				}
			})
		}
	}
	if !sameNativeEndpoint(`//./PIPE/CUA-DRIVER`, `\\.\pipe\cua-driver`, "", "windows") {
		t.Fatal("Windows pipe case/separator alias escaped reservation")
	}
	if runtime.GOOS == "windows" {
		return // Native symlink privileges are not required by the default suite.
	}
	real := filepath.Join(home, "native")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if !sameNativeEndpoint(filepath.Join(alias, "future.sock"), filepath.Join(real, "future.sock"), home, runtime.GOOS) {
		t.Fatal("missing socket under an aliased parent escaped reservation")
	}
	binary := filepath.Join(real, "cua-driver")
	if err := os.WriteFile(binary, []byte("never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "renamed")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	wire := mcpclient.StdioConfig{Executable: link, Dir: home, Env: map[string]string{"HOME": home}}
	if !duplicatesManagedCUA(wire, home, "darwin") {
		t.Fatal("executable symlink hid default CUA endpoint")
	}
	wire.Env["HOME"] = filepath.Join(home, "other-user")
	if duplicatesManagedCUA(wire, home, "darwin") {
		t.Fatal("independent HOME rejected")
	}
	wire.Executable = filepath.Join(home, "CuaDriverLocal.app", "Contents", "MacOS", "cua-driver")
	wire.Env["HOME"] = home
	if duplicatesManagedCUA(wire, home, "darwin") {
		t.Fatal("source-build namespace rejected")
	}
	installed := filepath.Join(home, ".aice", "bin", "cua", deps.CuaDriverVersion, "linux-"+runtime.GOARCH, "cua-driver")
	if err := os.MkdirAll(filepath.Dir(installed), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("not an executable"), 0600); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(home, "hardlink")
	if err := os.Link(installed, hardlink); err != nil {
		t.Fatal(err)
	}
	wire.Executable = hardlink
	if !duplicatesManagedCUA(wire, home, "linux") {
		t.Fatal("installed binary hard link escaped default endpoint admission")
	}
	local := filepath.Join(home, "cua-driver-local")
	if err := os.Link(installed, local); err != nil {
		t.Fatal(err)
	}
	wire.Executable = local
	if duplicatesManagedCUA(wire, home, "linux") {
		t.Fatal("local product name must select its independent namespace even for the same inode")
	}
}

func TestManagedCUAConnectionAdmissionSurvivesSettingsPublication(t *testing.T) {
	runMCPSettingsFixture(t, `{"transport":"http","url":"https://example.com/mcp"}`, func(ctx context.Context, s *interactiveSession) {
		home, err := s.application.userHome()
		if err != nil {
			t.Fatal(err)
		}
		next, err := s.configuration.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": {
			Transport: "stdio", Command: filepath.Join(home, "proxy"), Cwd: home, Args: []string{"--socket", desktopServiceEndpoint(home)},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		next = ownerTestApprove(t, next, "user:docs", config.MCPConnectionAllow)
		s.application.dependencies.openMCP = func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
			t.Fatal("replacement owner bypassed native reservation")
			return nil, nil
		}
		if err := s.publishMCPConfiguration(next); err != nil {
			t.Fatal(err)
		}
		if _, err := s.mcp.Connections()["user:docs"].Tools(ctx); err == nil {
			t.Fatal("replacement owner admitted duplicate")
		}
		if s.mcp.Status()[0].Detail != errManagedCUAConnection.Error() {
			t.Fatal("replacement owner lost admission reason")
		}
	})
}

func TestManagedCUAConnectionAdmissionAndManagementCLI(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		home := t.TempDir()
		c := ownerTestConfig(t, 0)
		c.DesktopEnabled = enabled
		definition := config.MCPServerSettings{Transport: "stdio", Command: filepath.Join(home, "proxy"), Cwd: home, Args: []string{"mcp", "--socket", desktopServiceEndpoint(home)}}
		var err error
		c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"duplicate": definition}})
		if err != nil {
			t.Fatal(err)
		}
		opens := 0
		deps := dependencies{userHomeDir: func() (string, error) { return home, nil }, openMCP: func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
			opens++
			return &mcpOwnedFixture{}, nil
		}, loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) {
			t.Fatal("MCP management created a model")
			return nil, nil
		}}
		a := &application{dependencies: deps}
		owner, err := a.newConfiguredMCPOwner(c, ownerTestGuard(t), true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Connections()["user:duplicate"].Tools(t.Context()); err == nil || opens != 0 {
			t.Fatal("yolo opened a duplicate native connection", err, opens)
		}
		status := owner.Status()[0]
		if status.State != "disabled" || !strings.Contains(status.Detail, "/desktop") || status.CatalogKnown || owner.slots != 0 {
			t.Fatal("rejection lost status or leaked a connection slot", status)
		}
		_ = owner.Close()
		// The actual command must reject both initial registration and replacement
		// before writing settings, and must not initialize any optional service.
		for _, action := range []string{"add", "replace"} {
			command, err := newTestCommand(t, deps)
			if err != nil {
				t.Fatal(err)
			}
			key := "new-service"
			if action == "replace" {
				key = "user:duplicate"
			}
			command.SetArgs([]string{"mcp", action, key})
			raw, _ := json.Marshal(definition)
			command.SetIn(bytes.NewReader(raw))
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			if err := command.ExecuteContext(t.Context()); !errors.Is(err, errManagedCUAConnection) {
				t.Fatal("CLI did not explain reserved native connection", err)
			}
			if _, err := os.Stat(c.Paths.GlobalSettings); !os.IsNotExist(err) || opens != 0 {
				t.Fatal("rejected registration wrote settings or connected", err, opens)
			}
		}
	}
}

func TestManagedCUADuplicatePrintDiscoveryDoesNotLaunch(t *testing.T) {
	home := t.TempDir()
	c := ownerTestConfig(t, 0)
	c.DeepSeekAPIKey = "offline-fixture"
	var err error
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {
		Transport: "stdio", Command: filepath.Join(home, "proxy"), Cwd: home, Args: []string{"--socket", desktopServiceEndpoint(home)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := &mcpStartupModel{t: t, search: true, wantRemote: false}
	command, err := newTestCommand(t, dependencies{
		loadConfig:  func(config.LoadOptions) (config.Config, error) { return c, nil },
		userHomeDir: func() (string, error) { return home, nil },
		newModel:    func(config.Config) (llm.Streamer, error) { return model, nil },
		openMCP: func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
			t.Fatal("Print launched a duplicate connection")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", t.TempDir(), "--yolo", "--print", "discover"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestManagedCUAIndependentConnectionStillNeedsApproval(t *testing.T) {
	home := t.TempDir()
	c := ownerTestConfig(t, 0)
	var err error
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"independent": {
		Transport: "stdio", Command: filepath.Join(home, "cua-driver"), Cwd: home, Args: []string{"mcp", "--direct"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	opens := 0
	a := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return home, nil }, openMCP: func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
		opens++
		return &mcpOwnedFixture{}, nil
	}}}
	owner, err := a.newConfiguredMCPOwner(c, ownerTestGuard(t), false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Connections()["user:independent"].Tools(t.Context()); err == nil || opens != 0 {
		t.Fatal("independent CUA inherited managed approval")
	}
	c = ownerTestApprove(t, c, "user:independent", config.MCPConnectionAllow)
	owner2, err := a.newConfiguredMCPOwner(c, ownerTestGuard(t), false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner2.Close()
	if _, err := owner2.Connections()["user:independent"].Tools(t.Context()); err != nil || opens != 1 {
		t.Fatal("approved independent runtime blocked", err)
	}
}
