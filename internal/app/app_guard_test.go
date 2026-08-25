package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestApplicationInteractiveFirstLoginKeepsWorkspaceGuard(t *testing.T) {
	t.Parallel()

	const secret = "guard-secret"
	workspace := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workspace, ".env"),
		[]byte(secret),
		0o600,
	); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	call := llm.ToolCall{
		ID:        "read-1",
		Name:      "read",
		Arguments: []byte(`{"path":".env"}`),
	}
	model := &toolLoopModel{firstCall: &call}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		saveSetting: func(config.Setting, string) error { return nil },
		saveAPIKey: func(string, string) (string, error) {
			return "/global/auth.json", nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			_ tui.Options,
		) error {
			commandRunner := runner.(tui.SlashCommandRunner)
			if _, err := commandRunner.RunSlashCommand(
				ctx,
				tui.SlashCommandRequest{
					Name:   "login",
					Secret: "test-key",
				},
			); err != nil {
				return err
			}
			return runInteractive(ctx, runner, "inspect", nil)
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want tool call and completion", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) == 0 {
		t.Fatal("second model request has no messages")
	}
	result, ok := messages[len(messages)-1].(llm.ToolResultMessage)
	if !ok {
		t.Fatalf("last message = %T, want ToolResultMessage", messages[len(messages)-1])
	}
	if !result.IsError {
		t.Fatalf(".env tool result = %#v, want guard denial", result)
	}
	for _, part := range result.Content {
		if strings.Contains(part.Text, secret) {
			t.Fatalf(".env contents reached model context: %q", part.Text)
		}
	}
}

func TestHandleGuardAskAllowAlwaysGrantsPathNotParent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outsideDir := t.TempDir()
	granted := filepath.Join(outsideDir, "granted.txt")
	sibling := filepath.Join(outsideDir, "sibling.txt")

	gate, err := guard.New(workspace, guard.Config{})
	if err != nil {
		t.Fatalf("guard.New() error = %v", err)
	}

	session := &interactiveSession{
		guard:         gate,
		guardRequests: make(chan interaction.GuardRequest, 1),
	}
	go func() {
		request := <-session.guardRequests
		request.Reply <- interaction.GuardDecisionAllowAlways
	}()

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: mustFilePathArgs(t, granted),
	}
	decision, err := session.handleGuardAsk(t.Context(), call, agent.GuardResult{
		Decision: agent.GuardAsk,
		RuleID:   "pathAccess.ask",
		Action: agent.GuardAction{
			Kind:     "file",
			Path:     granted,
			ToolName: "read",
		},
	})
	if err != nil {
		t.Fatalf("handleGuardAsk() error = %v", err)
	}
	if decision != agent.GuardAllow {
		t.Fatalf("handleGuardAsk() decision = %q, want allow", decision)
	}

	grantedResult, err := gate.Check(t.Context(), llm.ToolCall{
		ID:        "call-2",
		Name:      "read",
		Arguments: mustFilePathArgs(t, granted),
	})
	if err != nil {
		t.Fatalf("Check(granted) error = %v", err)
	}
	if grantedResult.Decision != guard.DecisionAllow {
		t.Fatalf("granted path decision = %q, want allow", grantedResult.Decision)
	}

	siblingResult, err := gate.Check(t.Context(), llm.ToolCall{
		ID:        "call-3",
		Name:      "read",
		Arguments: mustFilePathArgs(t, sibling),
	})
	if err != nil {
		t.Fatalf("Check(sibling) error = %v", err)
	}
	if siblingResult.Decision != guard.DecisionAsk {
		t.Fatalf("sibling path decision = %q, want ask", siblingResult.Decision)
	}
}

func mustFilePathArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"file_path": path})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
