package app

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestPrintCompactionSharesFrozenServiceWithoutReloadingSettings(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	info := llm.Model{ID: "original", Name: "Original", API: llm.API("test-api"), Provider: llm.ProviderID("test-provider"), ContextWindow: 10000, MaxTokens: 1000}
	store := createAppTestSession(t, path, workspace)
	prompt, err := llm.NewUserMessage(llm.NewTextContent("inspect").Part())
	if err != nil {
		t.Fatal(err)
	}
	answer := llm.NewAssistantMessage(info)
	answer.Content = []llm.ContentPart{llm.NewTextContent(strings.Repeat("source transcript ", 8000)).Part()}
	answer.StopReason = llm.StopReasonStop
	if err := appendTestSessionMessages(t.Context(), store, []llm.AgentMessage{prompt, answer}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	model := &controlledModel{response: "summary", stopReason: llm.StopReasonStop}
	global, home := compactPrintConfig(t, info)
	initial := global
	loads, factories := 0, 0
	command, err := newTestCommand(t, dependencies{
		loadConfig: func() (config.Config, error) { loads++; return global, nil },
		newModel: func(configuration config.Config) (agent.Model, error) {
			factories++
			if configuration.Model != initial.Model {
				t.Fatalf("factory used changed settings: %#v", configuration)
			}
			global.Model = "changed-externally"
			return model, nil
		},
		providers:                  []provider.Provider{&compactTestProvider{model: info, service: model}},
		compactionKeepRecentTokens: 20000,
		userHomeDir:                func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace, "--session", path, "--print", "continue"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if loads != 1 || factories != 1 || len(model.requests) != 2 {
		t.Fatalf("loads/factories/requests = %d/%d/%d", loads, factories, len(model.requests))
	}
	for _, request := range model.requests {
		if request.Model.ID != info.ID {
			t.Fatal("model changed during run")
		}
	}
	if model.requests[0].SystemPrompt != compactionSystemPrompt || !strings.HasPrefix(model.requests[1].SystemPrompt, "compact test") {
		t.Fatal("summary and custom main prompt ownership changed")
	}
}

func TestInteractiveCompactionsReuseRunSettingsAfterExternalChange(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	runPrintTurn(t, workspace, path, "old question", "old answer")
	info := llm.Model{ID: "original", Name: "Original", API: llm.API("test-api"), Provider: llm.ProviderID("test-provider"), ContextWindow: 10000, MaxTokens: 1000}
	model := &controlledModel{response: "answer", stopReason: llm.StopReasonStop, usage: llm.Usage{TotalTokens: 9000}}
	global, home := compactPrintConfig(t, info)
	initial := global
	loads, factories := 0, 0
	command, err := newTestCommand(t, dependencies{
		loadConfig: func() (config.Config, error) { loads++; return global, nil },
		newModel: func(configuration config.Config) (agent.Model, error) {
			factories++
			if configuration.Model != initial.Model || configuration.CustomBaseURL != initial.CustomBaseURL {
				t.Fatalf("factory used external changes: %#v", configuration)
			}
			return model, nil
		},
		providers:                  []provider.Provider{&compactTestProvider{model: info, service: model}},
		compactionKeepRecentTokens: 1,
		userHomeDir:                func() (string, error) { return home, nil },
		runTUI: func(ctx context.Context, runner interaction.Runner, _ tui.Options) error {
			active, err := runner.NewRun(context.Background(), interaction.RunInput{Prompt: "initial"}, func(_ context.Context, event interaction.Event) error {
				if event.Kind == interaction.EventAssistantEnd {
					global.Model = "externally-selected-model"
					global.CustomBaseURL = "https://changed.invalid"
				}
				return nil
			})
			if err != nil {
				return err
			}
			for _, text := range []string{"follow one", "follow two"} {
				if err := active.Deliver(context.Background(), interaction.Delivery{ID: text, Text: text, Kind: interaction.DeliveryKindFollowUp}); err != nil {
					return err
				}
			}
			return active.Run(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace, "--session", path})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if loads != 1 || factories != 2 || len(model.requests) != 5 {
		t.Fatalf("loads/factories/requests = %d/%d/%d", loads, factories, len(model.requests))
	}
	snapshot := openSessionSnapshot(t, path)
	if len(snapshot.Compactions) != 2 {
		t.Fatalf("compactions = %d", len(snapshot.Compactions))
	}
	for _, request := range model.requests {
		if request.Model.ID != info.ID {
			t.Fatal("request model followed external settings")
		}
	}
	for _, index := range []int{1, 3} {
		if model.requests[index].SystemPrompt != compactionSystemPrompt {
			t.Fatal("summary prompt changed")
		}
	}
}
