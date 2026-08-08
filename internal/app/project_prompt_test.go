package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestResolveProjectContextNoResourcesUsesDefaultPrompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	paths := trustTestPaths(t)
	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextIgnoresCorruptStoreWithoutResources(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	paths := trustTestPaths(t)
	if err := os.WriteFile(paths.GlobalTrust, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextDeniedSkipsProjectAppend(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(false),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default (denied project)",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextAskWithoutUIIsDenied(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default (ask without UI)",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextApprovedAppendsProjectPrompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	ws := testWorkspace(t, workspace)
	want := defaultPromptFor(t, ws) + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project rules"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextApprovedReplacesBaseWithSystemPrompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/SYSTEM.md", "project base")
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project notes")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	ws := testWorkspace(t, workspace)
	want := "project base" + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project notes"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextBlankAppendAddsNothing(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "   \n  ")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default for blank append",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextGlobalPromptsApplyWithoutProjectResources(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	paths := trustTestPaths(t)
	globalDir := filepath.Dir(paths.GlobalSettings)
	writeAppFile(t, globalDir, "SYSTEM.md", "global base")
	writeAppFile(t, globalDir, "APPEND_SYSTEM.md", "global notes")

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	want := "global base" + "\n\n" +
		"Project instructions from " + filepath.Join(globalDir, "APPEND_SYSTEM.md") +
		":\n" + "global notes"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextGlobalPromptFallsBackToDefault(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	paths := trustTestPaths(t)
	globalDir := filepath.Dir(paths.GlobalSettings)
	writeAppFile(t, globalDir, "SYSTEM.md", "global base")
	writeAppFile(t, globalDir, "APPEND_SYSTEM.md", "   ")

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != "global base" {
		t.Errorf("systemPrompt = %q, want global base only", project.systemPrompt)
	}
}

func TestResolveProjectContextStoredDenyBeatsAlways(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)
	key, err := trust.CanonicalPath(workspace)
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	if err := trust.NewStore(paths.GlobalTrust).Set(key, trust.DecisionUntrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	configuration := trustTestConfig(paths)
	configuration.DefaultProjectTrust = trust.DefaultAlways

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		configuration,
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default (stored deny)",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextStoredTrustLoadsPrompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)
	key, err := trust.CanonicalPath(workspace)
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	if err := trust.NewStore(paths.GlobalTrust).Set(key, trust.DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	ws := testWorkspace(t, workspace)
	want := defaultPromptFor(t, ws) + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project rules"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextAskWithUIPersistsAndLoads(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	var gotCWD string
	askUI := func(cwd string) (trust.Choice, error) {
		gotCWD = cwd
		return trust.Choice{
			Label:    "Trust",
			Decision: trust.DecisionTrusted,
			Updates:  []trust.Update{{Path: cwd, Decision: trust.DecisionTrusted}},
		}, nil
	}
	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		nil,
		askUI,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if !project.trust.Prompted {
		t.Error("trust prompt not shown")
	}
	ws := testWorkspace(t, workspace)
	if gotCWD != ws.PhysicalPath() {
		t.Errorf("AskUI cwd = %q, want %q", gotCWD, ws.PhysicalPath())
	}
	key, err := trust.CanonicalPath(workspace)
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	entry, found, err := trust.NewStore(paths.GlobalTrust).Lookup(key)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Decision != trust.DecisionTrusted {
		t.Errorf("stored decision = %#v, want trusted", entry)
	}
	want := defaultPromptFor(t, ws) + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project rules"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextApprovedAppendsAgentsFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, "AGENTS.md", "agent guidance")
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	ws := testWorkspace(t, workspace)
	want := defaultPromptFor(t, ws) + "\n\n" +
		fmt.Sprintf(projectAgentsBoundaryLabel, filepath.Join(ws.PhysicalPath(), "AGENTS.md")) +
		"agent guidance" + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project rules"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextApprovedOrdersAgentsBeforeAppend(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/SYSTEM.md", "project base")
	writeAppFile(t, workspace, "AGENTS.md", "agent guidance")
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md", "project rules")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	ws := testWorkspace(t, workspace)
	want := "project base" + "\n\n" +
		fmt.Sprintf(projectAgentsBoundaryLabel, filepath.Join(ws.PhysicalPath(), "AGENTS.md")) +
		"agent guidance" + "\n\n" +
		projectAppendBoundary(ws.PhysicalPath(), ".aice/APPEND_SYSTEM.md") +
		"project rules"
	if project.systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", project.systemPrompt, want)
	}
}

func TestResolveProjectContextDeniedSkipsAgentsFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, "AGENTS.md", "agent guidance")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(false),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default (denied project)",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextBlankAgentsAddsNothing(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, "AGENTS.md", "   \n  ")
	paths := trustTestPaths(t)

	project, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err != nil {
		t.Fatalf("resolveProjectContext() error = %v", err)
	}
	if project.systemPrompt != defaultPromptFor(t, testWorkspace(t, workspace)) {
		t.Errorf(
			"systemPrompt = %q, want default for blank AGENTS.md",
			project.systemPrompt,
		)
	}
}

func TestResolveProjectContextRejectsOversizedPrompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeAppFile(t, workspace, ".aice/APPEND_SYSTEM.md",
		strings.Repeat("x", maxProjectPromptBytes+1))
	paths := trustTestPaths(t)

	_, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("resolveProjectContext() error = %v, want size error", err)
	}
}

func TestResolveProjectContextRejectsNonUTF8Prompt(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".aice"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, ".aice", "APPEND_SYSTEM.md"),
		[]byte{0xff, 0xfe, 0x00},
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	paths := trustTestPaths(t)

	_, err := newTestApplication().resolveProjectContext(
		t.Context(),
		testWorkspace(t, workspace),
		trustTestConfig(paths),
		boolPtr(true),
		nil,
		testBuiltInTools(t, testWorkspace(t, workspace)),
	)
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("resolveProjectContext() error = %v, want UTF-8 error", err)
	}
}

func newTestApplication() *application {
	return &application{}
}

// testBuiltInTools builds the real built-in tool set for the workspace so
// prompt assembly exercises the same snippets and guidelines as production.
func testBuiltInTools(t *testing.T, workspace *tool.Workspace) []agent.Tool {
	t.Helper()
	tools, err := newBuiltInTools(workspace)
	if err != nil {
		t.Fatalf("newBuiltInTools() error = %v", err)
	}
	return tools
}

// defaultPromptFor builds the expected built-in default system prompt for the
// workspace used by resolveProjectContext tests.
func defaultPromptFor(t *testing.T, workspace *tool.Workspace) string {
	t.Helper()
	return buildDefaultSystemPrompt(testBuiltInTools(t, workspace), workspace.Path())
}

func boolPtr(value bool) *bool {
	return &value
}

func testWorkspace(t *testing.T, path string) *tool.Workspace {
	t.Helper()
	workspace, err := tool.NewWorkspace(path)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	return workspace
}

func trustTestPaths(t *testing.T) config.Paths {
	t.Helper()
	root := t.TempDir()
	return config.Paths{
		GlobalSettings: filepath.Join(root, "settings.json"),
		GlobalAuth:     filepath.Join(root, "auth.json"),
		GlobalTrust:    filepath.Join(root, "trust.json"),
		BinDir:         filepath.Join(root, "bin"),
	}
}

func trustTestConfig(paths config.Paths) config.Config {
	return config.Config{
		Provider:            string(deepseek.ProviderID),
		Model:               deepseek.ModelV4Flash,
		DeepSeekAPIKey:      "test-key",
		DefaultProjectTrust: trust.DefaultAsk,
		Paths:               paths,
	}
}

func projectAppendBoundary(base, rel string) string {
	return fmt.Sprintf(projectAppendBoundaryLabel, filepath.Join(base, rel))
}

func writeAppFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
