package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestParseMCPServersConvertsPortableEntries(t *testing.T) {
	servers, skipped, err := config.ParseMCPServers([]byte(`{"mcpServers": {
		"GitHub": {"command": "npx", "args": ["-y", "server-github"], "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}", "MODE": "read"}},
		"docs.remote": {"type": "streamable-http", "url": "https://example.com/mcp", "headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}},
		"off": {"command": "/bin/server", "disabled": true},
		"literal": {"url": "https://example.com/mcp", "headers": {"Authorization": "Bearer secret"}},
		"legacy": {"type": "sse", "url": "https://example.com/sse"},
		"expanded": {"command": "npx", "args": ["${HOME}/x"]},
		"cua": {"command": "cua-driver", "args": ["mcp"]}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	mode := "read"
	want := map[string]config.MCPServerSettings{
		"github": {Name: "GitHub", Transport: "stdio", Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]config.MCPValueRef{
			"GITHUB_TOKEN": {Env: "GITHUB_TOKEN"}, "MODE": {Value: &mode},
		}},
		"docs-remote": {Name: "docs.remote", Transport: "http", URL: "https://example.com/mcp", Headers: map[string]config.MCPValueRef{
			"Authorization": {Env: "DOCS_TOKEN", Prefix: "Bearer "},
		}},
		"off": {Enabled: new(bool), Transport: "stdio", Command: "/bin/server"},
	}
	if !reflect.DeepEqual(servers, want) {
		t.Fatalf("servers = %#v", servers)
	}
	if len(skipped) != 4 {
		t.Fatalf("skipped = %v", skipped)
	}
	for _, name := range []string{"literal", "legacy", "expanded", "cua"} {
		if !strings.Contains(strings.Join(skipped, "\n"), `"`+name+`"`) {
			t.Fatalf("%s was not reported: %v", name, skipped)
		}
	}
	if strings.Contains(strings.Join(skipped, "\n"), "secret") {
		t.Fatal("skipped reason quoted a literal credential")
	}
	if _, _, err := config.ParseMCPServers([]byte(`{"other": {}}`)); err == nil {
		t.Fatal("document without a server collection was accepted")
	}
}

func TestParseMCPServerDefinitionKeepsNativeStrict(t *testing.T) {
	if _, err := config.ParseMCPServerDefinition([]byte(`{"transport":"http","url":"https://example.com","unknown":"x"}`)); err == nil {
		t.Fatal("native definition with an unknown field was accepted")
	}
	got, err := config.ParseMCPServerDefinition([]byte(`{"command":"uvx","args":["server"]}`))
	if err != nil || got.Transport != "stdio" || got.Command != "uvx" {
		t.Fatalf("portable definition = %#v, %v", got, err)
	}
	for _, command := range []string{"./server", "bin/server", ".."} {
		if _, err := config.ParseMCPServerDefinition([]byte(`{"command":"` + command + `"}`)); err == nil {
			t.Fatalf("relative command %q was accepted", command)
		}
	}
}

func TestMCPBareCommandResolvesOnPATHAndDefaultsToWorkspace(t *testing.T) {
	bin := t.TempDir()
	executable := filepath.Join(bin, "mcp-tool")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "relative-dir"+string(os.PathListSeparator)+bin)
	paths := testPaths(t.TempDir())
	workspace := t.TempDir()
	paths.ProjectSettings = filepath.Join(workspace, ".aice", "settings.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{
		"tool":    {Transport: "stdio", Command: "mcp-tool"},
		"missing": {Transport: "stdio", Command: "no-such-mcp-tool"},
	}}})
	c := loadMCP(t, paths, config.LoadOptions{Environment: true})
	tool := c.MCP.Servers["user:tool"]
	if tool.Executable != executable || tool.WorkingDir != workspace {
		t.Fatalf("launch = %q in %q", tool.Executable, tool.WorkingDir)
	}
	if missing := c.MCP.Servers["user:missing"]; missing.Executable != "" {
		t.Fatalf("unresolved command = %q", missing.Executable)
	}

	// The approval binds the resolved executable, not only the command name.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "mcp-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	moved := loadMCP(t, paths, config.LoadOptions{Environment: true}).MCP.Servers["user:tool"]
	if moved.Fingerprint == tool.Fingerprint || moved.CredentialScope != tool.CredentialScope {
		t.Fatal("a different resolved executable kept the approval fingerprint")
	}
}

func TestProjectMCPFileLoadsOnlyWhenTrusted(t *testing.T) {
	paths := testPaths(t.TempDir())
	workspace := t.TempDir()
	paths.ProjectSettings = filepath.Join(workspace, ".aice", "settings.json")
	if err := os.WriteFile(filepath.Join(workspace, ".mcp.json"), []byte(`{"mcpServers": {
		"docs": {"type": "http", "url": "https://example.com/mcp"},
		"bad": {"type": "sse", "url": "https://example.com/sse"}
	}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted := loadMCP(t, paths, config.LoadOptions{TrustProject: func(config.Paths, trust.Default) (bool, error) { return true, nil }})
	var project config.MCPServer
	for _, server := range trusted.MCP.Servers {
		project = server
	}
	if len(trusted.MCP.Servers) != 1 || project.Source.Kind != "project" || project.Source.Location != filepath.Join(workspace, ".mcp.json") || project.ID != "docs" {
		t.Fatalf("trusted servers = %#v", trusted.MCP.Servers)
	}
	if !strings.Contains(strings.Join(trusted.Diagnostics, "\n"), `"bad"`) {
		t.Fatalf("diagnostics = %v", trusted.Diagnostics)
	}
	// Project-file identities accept connection decisions like settings-file ones.
	if err := config.SaveMCPConnectionDecision(t.Context(), paths, project, config.MCPConnectionAllow); err != nil {
		t.Fatal(err)
	}
	untrusted := loadMCP(t, paths, config.LoadOptions{TrustProject: func(config.Paths, trust.Default) (bool, error) { return false, nil }})
	if len(untrusted.MCP.Servers) != 0 {
		t.Fatalf("untrusted project supplied servers: %#v", untrusted.MCP.Servers)
	}
}
