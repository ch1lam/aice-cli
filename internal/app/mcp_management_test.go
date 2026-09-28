package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func managementCommand(t *testing.T, paths config.Paths, input string, args ...string) (interaction.MCPResult, error) {
	t.Helper()
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel: func(config.Config) (llm.Streamer, error) {
			t.Error("management created a model")
			return nil, fmt.Errorf("unexpected model creation")
		},
		ensureHelpers: func(context.Context, deps.Options) error {
			t.Error("management attempted helper installation")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs(append([]string{"mcp"}, args...))
	command.SetIn(strings.NewReader(input))
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	err = command.ExecuteContext(t.Context())
	var result interaction.MCPResult
	if out.Len() > 0 {
		if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
			t.Fatalf("invalid command result %q: %v", out.String(), decodeErr)
		}
	}
	return result, err
}

func TestMCPManagementCLIConfiguredApprovalAndConnect(t *testing.T) {
	paths := trustTestPaths(t)
	endpoint, initializes, calls, deletes := mcpStartupServer(t)
	definition := `{"transport":"http","url":"` + endpoint + `","exclude_tools":["read"]}`
	result, err := managementCommand(t, paths, definition, "add", "docs")
	if err != nil || !result.Committed || initializes.Load() != 0 {
		t.Fatalf("add: %+v %v", result, err)
	}
	result, err = managementCommand(t, paths, "", "status", "user:docs")
	if err != nil || len(result.Services) != 1 || result.Services[0].Approval != "ask" || result.Services[0].State != "needs_approval" || result.Services[0].CatalogKnown {
		t.Fatalf("status: %+v %v", result, err)
	}
	fingerprint := result.Services[0].Fingerprint
	if _, err := managementCommand(t, paths, "", "connect", "user:docs"); err == nil || initializes.Load() != 0 {
		t.Fatal("unapproved test connected")
	}
	if _, err := managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", "stale"); err == nil {
		t.Fatal("stale approval accepted")
	}
	result, err = managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", fingerprint)
	if err != nil || !result.Committed || initializes.Load() != 0 {
		t.Fatal("approve connected or did not save")
	}
	result, err = managementCommand(t, paths, "", "connect", "user:docs")
	if err != nil || len(result.Services) != 1 || result.Services[0].State != "ready" || !result.Services[0].CatalogKnown || result.Services[0].ToolCount != 1 || result.Services[0].EligibleTools != 0 {
		t.Fatalf("connect: %+v %v", result, err)
	}
	if initializes.Load() != 1 || deletes.Load() != 1 || calls.Load() != 0 {
		t.Fatal("connection test called a tool or leaked its session")
	}
	if _, err := managementCommand(t, paths, "", "reconnect", "user:docs"); err != nil || initializes.Load() != 2 || deletes.Load() != 2 {
		t.Fatal("fresh reconnect failed")
	}
	if _, err := managementCommand(t, paths, "", "disable", "user:docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := managementCommand(t, paths, "", "connect", "user:docs"); err == nil || initializes.Load() != 2 {
		t.Fatal("disabled service connected")
	}
	if _, err := managementCommand(t, paths, "", "enable", "user:docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := managementCommand(t, paths, "", "deny", "user:docs", "--fingerprint", fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := managementCommand(t, paths, "", "connect", "user:docs"); err == nil || initializes.Load() != 2 {
		t.Fatal("denied service connected")
	}
	if _, err := managementCommand(t, paths, "", "forget", "user:docs", "--fingerprint", fingerprint); err != nil {
		t.Fatal(err)
	}
	result, err = managementCommand(t, paths, "", "status", "user:docs")
	if err != nil || result.Services[0].Approval != "ask" {
		t.Fatal("forget did not clear decision")
	}
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err == nil {
		t.Fatal("add overwrote an existing service")
	}
	if _, err := managementCommand(t, paths, "", "remove", "user:docs"); err != nil {
		t.Fatal(err)
	}
	result, err = managementCommand(t, paths, "", "status")
	if err != nil || len(result.Services) != 0 {
		t.Fatal("remove did not take effect")
	}
}

func TestMCPManagementCLICredentialIsolationAndRotation(t *testing.T) {
	paths := trustTestPaths(t)
	definition := `{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":{"auth_ref":"token","prefix":"Bearer "}}}`
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	result, _ := managementCommand(t, paths, "", "status", "user:docs")
	fingerprint := result.Services[0].Fingerprint
	if _, err := managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := managementCommand(t, paths, "secret-value\n", "credential", "user:docs", "typo", "--fingerprint", fingerprint); err == nil {
		t.Fatal("unreferenced secret slot saved")
	}
	result, err := managementCommand(t, paths, "secret-value\n", "credential", "user:docs", "token", "--fingerprint", fingerprint)
	if err != nil || !result.Committed {
		t.Fatalf("credential: %+v %v", result, err)
	}
	result, err = managementCommand(t, paths, "", "status", "user:docs")
	data, _ := json.Marshal(result)
	if err != nil || strings.Contains(string(data), "secret-value") || result.Services[0].Fingerprint == fingerprint || result.Services[0].Approval != "ask" {
		t.Fatal("credential leaked or rotation preserved approval")
	}
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, headers := c.MCP.Servers["user:docs"].ConnectionValues()
	if headers["Authorization"] != "Bearer secret-value" {
		t.Fatal("credential not bound")
	}
	result, err = managementCommand(t, paths, "", "credential", "user:docs", "token", "--fingerprint", result.Services[0].Fingerprint, "--clear")
	if err != nil || !result.Committed {
		t.Fatal("credential clear failed")
	}
	after, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(after), "secret-value") {
		t.Fatal("credential retained after clear")
	}
}

func TestMCPManagementCLIProjectTrustAndReadonlyDefinitions(t *testing.T) {
	paths := trustTestPaths(t)
	workspace := t.TempDir()
	paths.ProjectSettings = filepath.Join(workspace, ".aice", "settings.json")
	if err := os.MkdirAll(filepath.Dir(paths.ProjectSettings), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"mcp":{"servers":{"docs":{"transport":"http","url":"https://example.com/mcp"}}}}`)
	if err := os.WriteFile(paths.ProjectSettings, original, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := managementCommand(t, paths, "", "status", "--workspace", workspace, "--no-trust-project")
	if err != nil || len(result.Services) != 0 {
		t.Fatal("untrusted project supplied services")
	}
	result, err = managementCommand(t, paths, "", "status", "--workspace", workspace, "--trust-project")
	if err != nil || len(result.Services) != 1 || result.Services[0].Approval != "ask" {
		t.Fatalf("trust became connection approval: %+v %v", result, err)
	}
	key := result.Services[0].Key
	for _, action := range []string{"remove", "disable", "enable"} {
		if _, err := managementCommand(t, paths, "", action, key, "--workspace", workspace, "--trust-project"); err == nil {
			t.Fatalf("%s edited project config", action)
		}
	}
	after, _ := os.ReadFile(paths.ProjectSettings)
	if !bytes.Equal(original, after) {
		t.Fatal("read-only source was edited")
	}
}

func TestMCPManagementCLIValidationAndFailedTestPreservesSave(t *testing.T) {
	paths := trustTestPaths(t)
	for _, tc := range []struct {
		input string
		args  []string
	}{
		{`{"transport":"http","url":"https://example.com","unknown":"secret"}`, []string{"add", "docs"}},
		{`{"transport":"http","url":"https://example.com"}`, []string{"add", "cua"}},
		{strings.Repeat("x", (1<<20)+1), []string{"add", "docs"}},
	} {
		if result, err := managementCommand(t, paths, tc.input, tc.args...); err == nil || result.Committed {
			t.Fatal("invalid definition saved")
		}
	}
	// Saving a nonexistent executable is inert; only an explicit approved test
	// attempts it, and that failure cannot roll back the successfully saved file.
	root := t.TempDir()
	definition, _ := json.Marshal(config.MCPServerSettings{Transport: "stdio", Command: filepath.Join(root, "missing"), Cwd: root})
	if result, err := managementCommand(t, paths, string(definition), "add", "local"); err != nil || !result.Committed {
		t.Fatal("inert stdio save failed")
	}
	result, _ := managementCommand(t, paths, "", "status", "user:local")
	if _, err := managementCommand(t, paths, "", "approve", "user:local", "--fingerprint", result.Services[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.GlobalSettings)
	result, err := managementCommand(t, paths, "", "connect", "user:local")
	if err == nil || result.Committed || result.Services[0].State != "failed" {
		t.Fatal("failed connection test reported success")
	}
	after, _ := os.ReadFile(paths.GlobalSettings)
	if !bytes.Equal(before, after) {
		t.Fatal("failed connection test rewrote configuration")
	}
	if cli.ExitCode(err) != 1 {
		t.Fatal("connection failure exit code")
	}
}

func TestMCPCreateOnlyConcurrentSave(t *testing.T) {
	paths := trustTestPaths(t)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := range 2 {
		wg.Go(func() {
			definition := config.MCPServerSettings{Transport: "http", URL: fmt.Sprintf("https://example.com/%d", i)}
			_, commit, err := config.SaveMCPPatch(t.Context(), paths, config.MCPPatch{CreateOnly: true, Servers: map[string]*config.MCPServerSettings{"docs": &definition}})
			results <- err == nil && commit.Committed
		})
	}
	wg.Wait()
	close(results)
	count := 0
	for saved := range results {
		if saved {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent add saved %d replacements", count)
	}
}

func TestMCPManagementRemoveDoesNotResurrectAccess(t *testing.T) {
	paths := trustTestPaths(t)
	definition := `{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":{"auth_ref":"token"}}}`
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	result, _ := managementCommand(t, paths, "", "status", "user:docs")
	if _, err := managementCommand(t, paths, "removed-secret", "credential", "user:docs", "token", "--fingerprint", result.Services[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	result, _ = managementCommand(t, paths, "", "status", "user:docs")
	if _, err := managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", result.Services[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	if result, err := managementCommand(t, paths, "", "remove", "user:docs"); err != nil || !result.Committed {
		t.Fatal("remove failed")
	}
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	result, err := managementCommand(t, paths, "", "status", "user:docs")
	if err != nil || result.Services[0].Approval != "ask" || result.Services[0].State != "needs_auth" {
		t.Fatal("re-added service inherited old access")
	}
	auth, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(auth), "removed-secret") {
		t.Fatal("removed credential remained in auth store")
	}
}

func TestMCPManagementRemoveReportsPartialCommit(t *testing.T) {
	paths := trustTestPaths(t)
	if _, err := managementCommand(t, paths, `{"transport":"http","url":"https://example.com/mcp"}`, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := c.MCP.Servers["user:docs"]
	if _, err := config.SaveMCPConnectionDecision(t.Context(), paths, server, config.MCPConnectionAllow); err != nil {
		t.Fatal(err)
	}
	c, err = config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another writer leaving invalid settings after the frozen snapshot
	// was loaded. The auth commit succeeds; the settings writer must preserve it.
	if err := os.WriteFile(paths.GlobalSettings, []byte(`{"mcp":broken}`), 0600); err != nil {
		t.Fatal(err)
	}
	next, result, err := executeMCPManagement(t.Context(), c, nil, interaction.MCPRequest{Action: "remove", Key: server.Key}, nil)
	if err == nil || !result.Committed || !strings.Contains(result.Message, "definition was not removed") || next.MCP.ConnectionDecision(server.Key) != "ask" {
		t.Fatalf("partial removal hidden: %+v %v", result, err)
	}
	auth, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(auth), server.Fingerprint) {
		t.Fatal("approval survived committed removal")
	}
	settings, _ := os.ReadFile(paths.GlobalSettings)
	if string(settings) != `{"mcp":broken}` {
		t.Fatal("failed settings write changed source")
	}
}

func TestMCPManagementReplaceInvalidatesApproval(t *testing.T) {
	paths := trustTestPaths(t)
	definition := `{"transport":"http","url":"https://example.com/mcp"}`
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	result, _ := managementCommand(t, paths, "", "status", "user:docs")
	fingerprint := result.Services[0].Fingerprint
	if _, err := managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", fingerprint); err != nil {
		t.Fatal(err)
	}
	replacement := `{"transport":"http","url":"https://other.example/mcp"}`
	if result, err := managementCommand(t, paths, replacement, "replace", "user:docs"); err != nil || !result.Committed {
		t.Fatal("replace failed")
	}
	result, err := managementCommand(t, paths, "", "status", "user:docs")
	if err != nil || result.Services[0].Approval != "ask" || result.Services[0].Fingerprint == fingerprint {
		t.Fatal("replacement retained old connection approval")
	}
	if _, err := managementCommand(t, paths, "", "approve", "user:docs", "--fingerprint", fingerprint); err == nil {
		t.Fatal("stale reviewed identity approved replacement")
	}
}

func TestMCPManagementConnectRejectsOldApprovalOwner(t *testing.T) {
	c := ownerTestConfig(t, 1)
	c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
	owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	denied := ownerTestApprove(t, c, "user:service0", config.MCPConnectionDeny)
	_, _, err = executeMCPManagement(t.Context(), denied, owner, interaction.MCPRequest{Action: "connect", Key: "user:service0"}, nil)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("old owner connected under a superseded approval")
	}
}

func TestMCPManagementCanceledConnectionPreservesExitStatus(t *testing.T) {
	c := ownerTestConfig(t, 1)
	c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
	started := make(chan struct{})
	owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), false, func(ctx context.Context, _ mcpclient.Config) (mcpOwnedConnection, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := executeMCPManagement(ctx, c, owner, interaction.MCPRequest{Action: "connect", Key: "user:service0"}, nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; cli.ExitCode(err) != 130 {
		t.Fatalf("canceled management exit status: %v", err)
	}
}
