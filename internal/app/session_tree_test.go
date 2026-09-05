package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestApplicationCheckoutCreatesBranchOnNextTurn(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspace, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspace, sessionPath, "old prompt", "old answer")

	before := openSessionSnapshot(t, sessionPath)
	firstID := before.Messages[1].ID
	oldID := before.Messages[3].ID

	treeCommand := newSessionTestCommand(t)
	treeOutput := new(bytes.Buffer)
	treeCommand.SetOut(treeOutput)
	treeCommand.SetArgs([]string{
		"session", "tree",
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := treeCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("tree ExecuteContext() error = %v", err)
	}
	if !strings.Contains(treeOutput.String(), "* message "+oldID) ||
		!strings.Contains(treeOutput.String(), `"old prompt"`) {
		t.Fatalf("tree output = %q, want active old branch", treeOutput.String())
	}

	checkoutCommand := newSessionTestCommand(t)
	checkoutOutput := new(bytes.Buffer)
	checkoutCommand.SetOut(checkoutOutput)
	checkoutCommand.SetArgs([]string{
		"session", "checkout",
		"--workspace", workspace,
		"--session", sessionPath,
		"--entry", firstID,
	})
	if err := checkoutCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("checkout ExecuteContext() error = %v", err)
	}
	if !strings.Contains(checkoutOutput.String(), "next message will branch") {
		t.Fatalf("checkout output = %q, want branch guidance", checkoutOutput.String())
	}

	branchModel := runPrintTurn(
		t,
		workspace,
		sessionPath,
		"new prompt",
		"new answer",
	)
	if len(branchModel.requests) != 1 {
		t.Fatalf("branch model requests = %d, want 1", len(branchModel.requests))
	}
	messages := branchModel.requests[0].Messages
	if len(messages) != 3 {
		t.Fatalf("branch request messages = %d, want first turn and new prompt", len(messages))
	}
	assertTextMessage(t, messages[0], llm.RoleUser, "first prompt")
	assertTextMessage(t, messages[1], llm.RoleAssistant, "first answer")
	assertTextMessage(t, messages[2], llm.RoleUser, "new prompt")

	after := openSessionSnapshot(t, sessionPath)
	if len(after.Messages) != 6 {
		t.Fatalf("stored messages = %d, want both branches", len(after.Messages))
	}
	if len(after.LeafMoves) != 1 {
		t.Fatalf("leaf moves = %d, want checkout record", len(after.LeafMoves))
	}
	newID := after.Messages[5].ID
	active, err := session.ActiveBranch(after)
	if err != nil {
		t.Fatalf("ActiveBranch() error = %v", err)
	}
	if got, want := appNodeIDs(active), []string{after.Messages[0].ID, firstID, after.Messages[4].ID, newID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active branch = %v, want %v", got, want)
	}
	oldBranch, err := session.Branch(after, oldID)
	if err != nil {
		t.Fatalf("Branch(old) error = %v", err)
	}
	if got, want := appNodeIDs(oldBranch), []string{after.Messages[0].ID, firstID, after.Messages[2].ID, oldID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("old branch = %v, want %v", got, want)
	}

	finalTree := newSessionTestCommand(t)
	finalOutput := new(bytes.Buffer)
	finalTree.SetOut(finalOutput)
	finalTree.SetArgs([]string{
		"session", "tree",
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := finalTree.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("final tree ExecuteContext() error = %v", err)
	}
	tree := finalOutput.String()
	if !strings.Contains(tree, "- message "+oldID) ||
		!strings.Contains(tree, "* message "+newID) {
		t.Fatalf("final tree output = %q, want inactive and active siblings", tree)
	}
}

func TestApplicationCheckoutRootAndMissingEntry(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspace, sessionPath, "prompt", "answer")

	rootCommand := newSessionTestCommand(t)
	rootCommand.SetOut(io.Discard)
	rootCommand.SetArgs([]string{
		"session", "checkout",
		"--workspace", workspace,
		"--session", sessionPath,
		"--entry", "root",
	})
	if err := rootCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("root checkout ExecuteContext() error = %v", err)
	}
	rootSnapshot := openSessionSnapshot(t, sessionPath)
	if rootSnapshot.LeafID != "" {
		t.Fatalf("root checkout leaf = %q, want root", rootSnapshot.LeafID)
	}

	missingCommand := newSessionTestCommand(t)
	missingCommand.SetOut(io.Discard)
	missingCommand.SetArgs([]string{
		"session", "checkout",
		"--workspace", workspace,
		"--session", sessionPath,
		"--entry", "missing",
	})
	err := missingCommand.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), session.ErrEntryNotFound.Error()) {
		t.Fatalf("missing checkout error = %v, want ErrEntryNotFound", err)
	}
	after := openSessionSnapshot(t, sessionPath)
	if len(after.LeafMoves) != 1 {
		t.Fatalf("leaf moves after missing checkout = %d, want unchanged", len(after.LeafMoves))
	}
}

func newSessionTestCommand(t *testing.T) *cobra.Command {
	t.Helper()

	command, err := newTestCommand(t, dependencies{
		loadConfig: func() (config.Config, error) {
			t.Fatal("configuration loaded for Session navigation")
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model created for Session navigation")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	return command
}

func appNodeIDs(nodes []session.Node) []string {
	ids := make([]string, len(nodes))
	for index, node := range nodes {
		ids[index] = node.ID
	}
	return ids
}

func TestSessionNavigationDoesNotRecoverIncompleteGroup(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "partial.jsonl")
	store := createAppTestSession(t, path, workspace)
	prompt, err := llm.NewUserMessage(llm.NewTextContent("work").Part())
	if err != nil {
		t.Fatal(err)
	}
	assistant := llm.NewAssistantMessage(deepseek.DefaultModel())
	assistant.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "pending", Name: "write", Arguments: []byte(`{}`)}}}
	assistant.StopReason = llm.StopReasonToolUse
	if err := appendTestSessionMessages(t.Context(), store, []llm.AgentMessage{prompt, assistant}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	if err := writeSessionTree(output, snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), snapshot.LeafID) {
		t.Fatal("tree omitted pending source node")
	}
	if _, err := checkoutSessionStore(t.Context(), store, snapshot.LeafID, io.Discard); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("already-current unsafe checkout = %v", err)
	}
	menu := (&interactiveSession{conversation: conversationState{store: store}}).checkoutMenu()
	for _, option := range menu.Options {
		if option.Arguments == snapshot.LeafID {
			t.Fatal("menu offers incomplete tool group")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	tree := newSessionTestCommand(t)
	tree.SetOut(io.Discard)
	tree.SetArgs([]string{"session", "tree", "--workspace", workspace, "--session", path})
	if err := tree.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only tree recovered or changed the source log")
	}
	wrongWorkspace, err := tool.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareSession(t.Context(), wrongWorkspace, path, false); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	after, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("workspace rejection happened after recovery mutation")
	}
}
