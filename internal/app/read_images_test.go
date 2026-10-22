package app

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

// A Settings rebuild composes tools from the Session base set, so the
// Session-bound image reader must survive it.
func TestInteractiveImageReaderSurvivesToolRebuild(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	call := llm.ToolCall{ID: "read-1", Name: "read", Arguments: []byte(`{"image_id":"missing"}`)}
	model := &toolLoopModel{firstCall: &call}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return config.Config{Paths: paths, Provider: "deepseek", DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) { return model, nil },
		runTUI: func(ctx context.Context, runner tui.Runner, _ tui.Options) error {
			settings := runner.(interaction.SettingsWriter)
			if _, err := settings.ApplySettings(ctx, interaction.SettingsRequest{Changes: []interaction.SettingChange{{
				ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool},
			}}}); err != nil {
				return err
			}
			return runInteractive(ctx, runner, "inspect", nil)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", t.TempDir()})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want tool call and completion", len(model.requests))
	}
	messages := model.requests[1].Messages
	result, ok := messages[len(messages)-1].(llm.ToolResultMessage)
	if !ok || !result.IsError {
		t.Fatalf("read result = %#v, want missing-image error", messages[len(messages)-1])
	}
	for _, part := range result.Content {
		if strings.Contains(part.Text, "lookup is unavailable") {
			t.Fatalf("rebuilt read tool lost the Session image lookup: %q", part.Text)
		}
	}
}
