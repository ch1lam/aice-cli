package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

func httpMCP() config.MCPServerSettings {
	return config.MCPServerSettings{Transport: "http", URL: "https://example.com/mcp", Headers: map[string]config.MCPValueRef{
		"Authorization": {AuthRef: "token", Prefix: "Bearer "},
	}}
}

func loadMCP(t *testing.T, paths config.Paths, options config.LoadOptions) config.Config {
	t.Helper()
	c, err := config.LoadFiles(paths, options)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMCPSourceAndCredentialIsolation(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": httpMCP()}}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": settings})
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp": settings, "mcp_services": map[string]any{"forged": "secret"}})
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": "provider-secret"})
	c := loadMCP(t, paths, config.LoadOptions{})
	if len(c.MCP.Servers) != 2 || len(c.Diagnostics) != 1 {
		t.Fatalf("source composition: servers=%d diagnostics=%v", len(c.MCP.Servers), c.Diagnostics)
	}
	user := c.MCP.Servers["user:docs"]
	var project config.MCPServer
	for _, s := range c.MCP.Servers {
		if s.Source.Kind == "project" {
			project = s
		}
	}
	if project.Key == "" || user.Fingerprint == project.Fingerprint || user.CredentialScope == project.CredentialScope {
		t.Fatal("same-name sources share identity")
	}
	commit, err := config.SaveMCPCredential(t.Context(), paths, user, "token", "user-secret")
	if err != nil || !commit.Committed {
		t.Fatalf("credential save: %+v %v", commit, err)
	}
	c = loadMCP(t, paths, config.LoadOptions{})
	_, headers := c.MCP.Servers[user.Key].ConnectionValues()
	_, projectHeaders := c.MCP.Servers[project.Key].ConnectionValues()
	if headers["Authorization"] != "Bearer user-secret" || len(projectHeaders) != 0 || len(c.MCP.Servers[project.Key].MissingValues) != 1 {
		t.Fatal("credential crossed source boundary")
	}
	if c.OpenAIAPIKey != "provider-secret" {
		t.Fatal("provider credential changed")
	}
	if data, _ := json.Marshal(c.MCP); strings.Contains(string(data), "user-secret") {
		t.Fatal("resolved secret serialized")
	}
	if strings.Contains(fmt.Sprint(c.MCP), "user-secret") {
		t.Fatal("resolved secret printed")
	}
	changed := httpMCP()
	changed.URL = "https://other.example/mcp"
	settings.Servers["docs"] = changed
	next, err := c.WithMCP(settings)
	if err != nil {
		t.Fatal(err)
	}
	_, headers = next.MCP.Servers[user.Key].ConnectionValues()
	if len(headers) != 0 || next.MCP.Servers[user.Key].CredentialScope == c.MCP.Servers[user.Key].CredentialScope {
		t.Fatal("new endpoint inherited old credential")
	}
	if commit, err := config.SaveMCPCredential(t.Context(), paths, config.MCPServer{Key: user.Key, ID: user.ID, Source: user.Source, Settings: changed, CredentialScope: user.CredentialScope}, "token", "bad"); err == nil || commit.Committed {
		t.Fatal("forged connection binding saved")
	}
}

func TestMCPFrozenEnvironmentIdentityAndClone(t *testing.T) {
	paths := testPaths(t.TempDir())
	server := httpMCP()
	server.Headers["Authorization"] = config.MCPValueRef{Env: "AICE_TEST_MCP_TOKEN", Prefix: "Bearer "}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}}})
	options := environmentOptions(t, map[string]string{"AICE_TEST_MCP_TOKEN": "first-secret"})
	c := loadMCP(t, paths, options)
	bound := c.MCP.Servers["user:docs"]
	if bound.ConnectTimeout != 15*time.Second || bound.CallTimeout != 60*time.Second {
		t.Fatal("timeouts not defaulted")
	}
	t.Setenv("AICE_TEST_MCP_TOKEN", "second-secret")
	fresh := loadMCP(t, paths, options)
	if fresh.MCP.Servers[bound.Key].Fingerprint == bound.Fingerprint {
		t.Fatal("credential/account change kept authorization identity")
	}
	server.Name = "new label"
	server.CallTimeout = "90s"
	next, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}})
	if err != nil {
		t.Fatal(err)
	}
	_, headers := next.MCP.Servers[bound.Key].ConnectionValues()
	if headers["Authorization"] != "Bearer first-secret" || next.MCP.Servers[bound.Key].Fingerprint != bound.Fingerprint {
		t.Fatal("runtime edit reread environment or changed identity for a label")
	}
	headers["Authorization"] = "modified"
	_, again := next.MCP.Servers[bound.Key].ConnectionValues()
	if again["Authorization"] != "Bearer first-secret" {
		t.Fatal("connection values alias snapshot")
	}
	changed, err := next.WithSettings(map[config.Setting]string{config.SettingModel: "other-model"})
	if err != nil || changed.MCP.Servers[bound.Key].Fingerprint != bound.Fingerprint {
		t.Fatalf("ordinary settings lost MCP: %v", err)
	}
	isolated := loadMCP(t, paths, config.LoadOptions{})
	if len(isolated.MCP.Servers[bound.Key].MissingValues) != 1 {
		t.Fatal("environment resolved without opt-in")
	}
}

func TestMCPTrustAndRestrictionsCannotElevate(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	server := httpMCP()
	server.Headers = nil
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}, Restrictions: []config.MCPRestriction{{Source: "*", Server: "docs", Tools: []string{"remove"}}}}})
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}, Restrictions: []config.MCPRestriction{{Source: "user", Server: "docs", Tools: []string{"write"}}}}})
	c := loadMCP(t, paths, config.LoadOptions{})
	if c.MCP.ToolAllowed("user:docs", "remove") || c.MCP.ToolAllowed("user:docs", "write") || !c.MCP.ToolAllowed("user:docs", "read") {
		t.Fatal("restrictions not composed")
	}
	for _, key := range c.MCP.ServerKeys() {
		if c.MCP.ToolAllowed(key, "remove") {
			t.Fatal("project lifted user deny")
		}
	}
	next, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}})
	if err != nil || next.MCP.ToolAllowed("user:docs", "write") {
		t.Fatalf("runtime edit lost project tightening: %v", err)
	}
	if removed, err := c.WithMCP(config.MCPSettings{}); err != nil || removed.MCP.ServerAllowed("user:docs") || len(removed.MCP.Servers) != 1 {
		t.Fatal("removed user identity fell through to same-name project")
	}
	untrusted := loadMCP(t, paths, config.LoadOptions{TrustProject: func(config.Paths, trustDefault) (bool, error) { return false, nil }})
	if len(untrusted.MCP.Servers) != 1 || !untrusted.MCP.ToolAllowed("user:docs", "write") {
		t.Fatal("untrusted project contributed MCP data")
	}
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp": map[string]any{"restrictions": []any{map[string]any{"source": "user", "server": "docs", "allow": true}}}})
	if _, err := config.LoadFiles(paths, config.LoadOptions{}); err == nil {
		t.Fatal("project allow accepted")
	}
}

func TestMCPStdioConfigIsInertAndDistinct(t *testing.T) {
	paths := testPaths(t.TempDir())
	root := t.TempDir()
	text := "constant"
	server := config.MCPServerSettings{Transport: "stdio", Command: filepath.Join(root, "nonexistent-program"), Cwd: root, Args: []string{"--mode", "test"}, Env: map[string]config.MCPValueRef{"MODE": {Value: &text}}, PinnedTools: []string{"read"}}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"local": server}}})
	c := loadMCP(t, paths, config.LoadOptions{}) // executable does not exist; loading does not start/probe it
	bound := c.MCP.Servers["user:local"]
	env, _ := bound.ConnectionValues()
	if env["MODE"] != "constant" {
		t.Fatal("explicit stdio environment missing")
	}
	clone := c.MCP.Clone()
	copied := clone.Servers[bound.Key]
	*copied.Settings.Env["MODE"].Value = "changed"
	copied.Settings.Args[0] = "changed"
	copied.Settings.PinnedTools[0] = "changed"
	if *bound.Settings.Env["MODE"].Value != "constant" || bound.Settings.Args[0] != "--mode" || bound.Settings.PinnedTools[0] != "read" {
		t.Fatal("MCP clone shares mutable settings")
	}
	server.Args = []string{"different"}
	next, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"local": server}})
	if err != nil || next.MCP.Servers[bound.Key].CredentialScope == bound.CredentialScope {
		t.Fatalf("stdio arguments did not change identity: %v", err)
	}
}

func TestMCPInvalidConfigurationDoesNotQuoteValues(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"secret-value":"secret-value"}`,
		`{"servers":{"cua":{"transport":"http","url":"https://example.com/mcp"}}}`,
		`{"servers":{"bad":{"transport":"http","url":"http://127.attacker.example/mcp"}}}`,
		`{"servers":{"bad":{"transport":"http","url":"https://user:secret-value@example.com/mcp"}}}`,
		`{"servers":{"bad":{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":{"value":"secret-value"}}}}}`,
		`{"servers":{"bad":{"transport":"http","url":"https://example.com/mcp","headers":{"Idempotency-Key":{"env":"TOKEN"}}}}}`,
		`{"servers":{"bad":{"transport":"stdio","command":"relative","cwd":"relative"}}}`,
		`{"servers":{"bad":{"transport":"http","url":"https://example.com/mcp","call_timeout":"secret-value"}}}`,
		`{"servers":{"bad":{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":{"auth_ref":"openai_api_key.provider"}}}}}`,
	} {
		paths := testPaths(t.TempDir())
		writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": json.RawMessage(raw)})
		_, err := config.LoadFiles(paths, config.LoadOptions{})
		if err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe configuration error: %v", err)
		}
	}
}

func TestMCPAtomicPatchesAndCredentialSnapshots(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "keep"})
	var group sync.WaitGroup
	errs := make(chan error, 4)
	for i := range 4 {
		group.Go(func() {
			server := httpMCP()
			_, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{Servers: map[string]*config.MCPServerSettings{fmt.Sprintf("server-%d", i): &server}})
			if err == nil && !commit.Committed {
				err = fmt.Errorf("not committed")
			}
			errs <- err
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	c := loadMCP(t, paths, config.LoadOptions{})
	if c.Model != "keep" || len(c.MCP.Servers) != 4 {
		t.Fatal("concurrent patch lost settings")
	}
	bound := c.MCP.Servers["user:server-0"]
	next, err := c.WithMCPCredential(bound.Key, bound.CredentialScope, "token", "saved-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, oldValues := c.MCP.Servers[bound.Key].ConnectionValues()
	_, newValues := next.MCP.Servers[bound.Key].ConnectionValues()
	if len(oldValues) != 0 || newValues["Authorization"] != "Bearer saved-secret" || next.MCP.Servers[bound.Key].Fingerprint == bound.Fingerprint {
		t.Fatal("credential snapshot mutated or identity not invalidated")
	}
	cleared, err := next.WithMCPCredential(bound.Key, bound.CredentialScope, "token", "")
	if err != nil || len(cleared.MCP.Servers[bound.Key].MissingValues) != 1 {
		t.Fatal("credential removal failed")
	}
	saved, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{Enabled: map[string]bool{"server-0": false}})
	if err != nil || !commit.Committed || len(saved.Servers) != 4 || saved.Servers["server-0"].URL != bound.Settings.URL {
		t.Fatalf("toggle patch: %v", err)
	}
	before, err := os.ReadFile(paths.GlobalSettings)
	if err != nil {
		t.Fatal(err)
	}
	if _, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{Enabled: map[string]bool{"missing": true}}); err == nil || commit.Committed {
		t.Fatal("invalid toggle committed")
	}
	after, _ := os.ReadFile(paths.GlobalSettings)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed save changed file")
	}
}

func TestMCPEmptyIncludeAndMalformedSaveArePreserved(t *testing.T) {
	paths := testPaths(t.TempDir())
	empty := []string{}
	server := httpMCP()
	server.Headers = nil
	server.IncludeTools = &empty
	saved, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{Servers: map[string]*config.MCPServerSettings{"docs": &server}})
	if err != nil || !commit.Committed || saved.Servers["docs"].IncludeTools == nil {
		t.Fatalf("save empty include: %v", err)
	}
	c := loadMCP(t, paths, config.LoadOptions{})
	if c.MCP.Servers["user:docs"].Settings.IncludeTools == nil || c.MCP.ToolAllowed("user:docs", "read") {
		t.Fatal("explicit empty include became all tools")
	}
	server.IncludeTools = nil
	off := false
	server.Enabled = &off
	next, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}})
	if err != nil || next.MCP.ServerAllowed("user:docs") || next.MCP.ToolAllowed("user:docs", "read") {
		t.Fatal("disabled instance remains eligible")
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": map[string]any{"bad-field": "preserve-me"}})
	before, _ := os.ReadFile(paths.GlobalSettings)
	if _, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{Servers: map[string]*config.MCPServerSettings{"docs": &server}}); err == nil || commit.Committed {
		t.Fatal("malformed existing MCP configuration overwritten")
	}
	after, _ := os.ReadFile(paths.GlobalSettings)
	if string(before) != string(after) {
		t.Fatal("malformed file changed")
	}
}
