package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInitCommandCreatesAgentsFileAndRecordsTrust(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	runner := initTestRunner(t, workspace, nil)

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "init",
	})
	if err != nil {
		t.Fatalf("/init error = %v", err)
	}
	if !strings.Contains(output, "Created AGENTS.md") {
		t.Errorf("/init output = %q, want created message", output)
	}
	data, err := os.ReadFile(filepath.Join(workspace.PhysicalPath(), "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	if got, want := string(data), "# Project\n"; got != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}

	entry, found, err := runner.trustStore.Lookup(workspace.PhysicalPath())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Decision != trust.DecisionTrusted {
		t.Errorf("stored decision = %#v, want trusted", entry)
	}
}

func TestInitCommandUpdatesExistingAgentsFileWithoutTrustChange(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	if err := os.WriteFile(
		filepath.Join(workspace.PhysicalPath(), "AGENTS.md"),
		[]byte("# Old guidance\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runner := initTestRunner(t, workspace, nil)

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "init",
	})
	if err != nil {
		t.Fatalf("/init error = %v", err)
	}
	if !strings.Contains(output, "Updated AGENTS.md") {
		t.Errorf("/init output = %q, want updated message", output)
	}
	data, err := os.ReadFile(filepath.Join(workspace.PhysicalPath(), "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	if got, want := string(data), "# Project\n"; got != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}

	if _, found, err := runner.trustStore.Lookup(workspace.PhysicalPath()); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if found {
		t.Errorf("stored decision found, want untouched for existing file")
	}
}

func TestInitCommandRequiresCredentials(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	runner := initTestRunner(t, workspace, nil)
	runner.loop = nil

	_, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "init",
	})
	if err == nil || !strings.Contains(err.Error(), "API key is not configured") {
		t.Fatalf("/init error = %v, want credential error", err)
	}
}

func TestInitCommandRejectsArguments(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	runner := initTestRunner(t, workspace, nil)

	_, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "init",
		Arguments: "extra",
	})
	if err == nil || !strings.Contains(err.Error(), "does not accept arguments") {
		t.Fatalf("/init error = %v, want usage error", err)
	}
}

func TestInitCommandPropagatesModelFailure(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	runner := initTestRunner(t, workspace, nil)
	model := &controlledModel{err: modelFailure}
	loop, err := agent.NewLoop(model, runner.tools)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	runner.loop = loop

	_, err = runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "init",
	})
	if err == nil || !strings.Contains(err.Error(), "modelFailure") {
		t.Fatalf("/init error = %v, want model failure", err)
	}
}

func TestInitCommandReportsMissingWrite(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	runner := initTestRunner(t, workspace, nil)
	loop, err := agent.NewLoop(&controlledModel{
		response:   "done",
		stopReason: llm.StopReasonStop,
	}, runner.tools)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	runner.loop = loop

	_, err = runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "init",
	})
	if err == nil || !strings.Contains(err.Error(), "without creating AGENTS.md") {
		t.Fatalf("/init error = %v, want missing file error", err)
	}
}

func TestInitCommandListedInCatalog(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{}
	commands := runner.SlashCommands()
	initCommand := interactiveSlashCommand(t, commands, "init")
	if initCommand.Description == "" {
		t.Error("/init description is empty")
	}
}

var modelFailure = errorString("modelFailure")

type errorString string

func (e errorString) Error() string { return string(e) }

// initTestRunner builds an interactiveSession whose loop can write AGENTS.md
// at the workspace root and whose trust store is isolated to a temp file.
func initTestRunner(t *testing.T, workspace *tool.Workspace, tools []agent.Tool) *interactiveSession {
	t.Helper()
	if tools == nil {
		write, err := tool.NewWrite(workspace)
		if err != nil {
			t.Fatalf("NewWrite() error = %v", err)
		}
		tools = []agent.Tool{write}
	}
	call := llm.ToolCall{
		ID:   "write-1",
		Name: "write",
		Arguments: json.RawMessage(
			`{"path":"AGENTS.md","content":"# Project\n"}`,
		),
	}
	loop, err := agent.NewLoop(&toolLoopModel{firstCall: &call}, tools)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	paths := trustTestPaths(t)
	return &interactiveSession{
		application:   &application{dependencies: dependencies{}},
		loop:          loop,
		model:         deepseek.DefaultModel(),
		configuration: trustTestConfig(paths),
		trustStore:    trust.NewStore(paths.GlobalTrust),
		providers:     defaultProviders(),
		workspace:     workspace,
		workspacePath: workspace.PhysicalPath(),
	}
}
