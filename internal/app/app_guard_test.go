package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
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
