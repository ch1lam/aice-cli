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
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveContextTracksToolsAndCompactionWithOverride(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeAppFile(t, workspace, "large.txt", strings.Repeat("x", 14000))
	info := llm.Model{ID: "test", Name: "Test", API: "test-api", Provider: "test-provider", ContextWindow: 1_000_000, MaxTokens: 1000}
	service := &roundCompactionModel{}
	configuration, home := compactPrintConfig(t, info)
	configuration.ContextWindows = map[string]int64{"test-provider/test": 10000}
	var sawUsage, sawToolGrowth, sawCompaction bool
	command, err := newTestCommand(t, dependencies{
		loadConfig:  func() (config.Config, error) { return configuration, nil },
		newModel:    func(config.Config) (agent.Model, error) { return service, nil },
		providers:   []provider.Provider{&compactTestProvider{model: info, service: service}},
		userHomeDir: func() (string, error) { return home, nil },
		runTUI: func(ctx context.Context, runner interaction.Runner, options tui.Options) error {
			if options.Context.Window != 10000 || !options.Context.Estimated || options.Context.Tokens != 0 {
				t.Fatalf("startup context = %+v", options.Context)
			}
			active, err := runner.NewRun(context.Background(), interaction.RunInput{Prompt: "inspect large.txt"}, func(_ context.Context, event interaction.Event) error {
				if event.Context == nil {
					return nil
				}
				usage := *event.Context
				if usage.Window != 10000 {
					t.Fatalf("run lost override: %+v", usage)
				}
				if event.Kind == interaction.EventAssistantEnd && !usage.Estimated && usage.Tokens == 9000 {
					sawUsage = true
				}
				if event.Kind == interaction.EventToolEnd && usage.Tokens > 9000 && usage.Estimated {
					sawToolGrowth = true
				}
				if event.Kind == interaction.EventAssistantStart && sawToolGrowth && usage.Tokens < 9000 && usage.Estimated {
					sawCompaction = true
				}
				return nil
			})
			if err != nil {
				return err
			}
			if err := active.Run(ctx); err != nil {
				return err
			}
			state := runner.(interaction.RuntimeStateProvider).RuntimeState()
			if state.Context.Tokens != 9000 || state.Context.Estimated {
				t.Fatalf("final context = %+v", state.Context)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace, "--yolo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if service.summaries != 1 || !sawUsage || !sawToolGrowth || !sawCompaction {
		t.Fatalf("summaries=%d usage=%t tool growth=%t compaction=%t", service.summaries, sawUsage, sawToolGrowth, sawCompaction)
	}
}

func TestContextWindowsFollowProviderAndModel(t *testing.T) {
	t.Parallel()
	configuration := config.Config{ContextWindows: map[string]int64{
		"openai-codex/gpt-5.6-terra": 272_000,
		"custom/Org/Model.v1":        32_768,
	}}
	for _, test := range []struct {
		provider, model string
		want            int64
	}{
		{"openai-codex", "gpt-5.6-terra", 272_000},
		{"openai", "gpt-5.6-terra", 1_050_000},
		{"opencode-go", "kimi-k2.6", 262_144},
		{"custom", "Org/Model.v1", 32_768},
	} {
		t.Run(test.provider+"/"+test.model, func(t *testing.T) {
			configuration.Provider, configuration.Model = test.provider, test.model
			model, _, err := resolveModelSettings(defaultProviders(), configuration)
			if err != nil {
				t.Fatal(err)
			}
			if model.ContextWindow != test.want || displayContextWindow(model, configuration) != test.want {
				t.Fatalf("window = %d, want %d", model.ContextWindow, test.want)
			}
		})
	}
	configuration.ContextWindows["openai-codex/gpt-5.6-terra"] = 1_000_000
	configuration.Provider, configuration.Model = "openai-codex", "gpt-5.6-terra"
	model, _, err := resolveModelSettings(defaultProviders(), configuration)
	if err != nil || model.ContextWindow != 1_000_000 {
		t.Fatalf("long context: %d, %v", model.ContextWindow, err)
	}
}

func TestContextDisplayUsesCurrentWindowNotCumulativeUsage(t *testing.T) {
	t.Parallel()
	model := llm.Model{ID: "model", Provider: "provider", API: "test-api", ContextWindow: 200_000}
	assistant := llm.NewAssistantMessage(model)
	assistant.Timestamp = 100
	assistant.StopReason = llm.StopReasonStop
	assistant.Content = []llm.ContentPart{llm.NewTextContent("answer").Part()}
	assistant.Usage = llm.Usage{InputTokens: 1000, CacheReadTokens: 8000, CacheWriteTokens: 500, OutputTokens: 500, TotalTokens: 10000}
	old := assistant
	old.Timestamp = 99
	old.Usage = llm.Usage{TotalTokens: 100000}
	history := []llm.AgentMessage{old, assistant}
	got := contextDisplay(model, config.Config{}, "system", nil, history)
	if got != (interaction.DisplayContext{Tokens: 10000, Window: 200000, Known: true}) {
		t.Fatalf("display = %+v", got)
	}
	user, err := llm.NewUserMessage(llm.NewTextContent("12345678").Part())
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, user)
	got = contextDisplay(model, config.Config{}, "system", nil, history)
	if got.Tokens != 10002 || !got.Estimated {
		t.Fatalf("trailing input = %+v", got)
	}
	model.ID = "different-model"
	got = contextDisplay(model, config.Config{}, "system", nil, history)
	if !got.Estimated || got.Tokens >= 10000 {
		t.Fatalf("reused another model's usage: %+v", got)
	}
	model.ID = "model"
	summary := llm.CompactionSummaryMessage{Role: llm.RoleCompactionSummary, Summary: "earlier work", TokensBefore: 100000, Timestamp: 200}
	got = contextDisplay(model, config.Config{}, "system", nil, []llm.AgentMessage{summary, assistant})
	if !got.Known || !got.Estimated || got.Tokens >= 10000 {
		t.Fatalf("stale pre-compaction usage: %+v", got)
	}
	got = contextDisplay(model, config.Config{}, "12345678", nil, nil)
	if got.Tokens != 0 || !got.Known {
		t.Fatalf("fresh context is not empty: %+v", got)
	}
}

func TestUnconfiguredContextUsesProviderDefault(t *testing.T) {
	t.Parallel()
	for _, configuration := range []config.Config{
		{Provider: "custom", Model: "local"},
		{Provider: "openai-codex", Model: "gpt-6-astra"},
		{Provider: "openai-codex", Model: "gpt-5.6-terra"},
		{Provider: "openai", OpenAIBaseURL: "https://gateway.example/v1"},
		{Provider: "deepseek", DeepSeekBaseURL: "https://gateway.example"},
		{Provider: "opencode-go", OpenCodeBaseURL: "https://gateway.example"},
	} {
		model, _, err := resolveModelSettings(defaultProviders(), configuration)
		if err != nil {
			t.Fatal(err)
		}
		if got := displayContextWindow(model, configuration); got != model.ContextWindow {
			t.Fatalf("%s window = %d, want default %d", model.Provider, got, model.ContextWindow)
		}
		if model.ContextWindow <= 0 {
			t.Fatal("lost fallback protection")
		}
	}
}

func TestCodexAndAPIDefaultContextWindowsDiffer(t *testing.T) {
	t.Parallel()
	for _, modelID := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		for _, test := range []struct {
			provider string
			window   int64
		}{
			{"openai-codex", 272000}, {"openai", 1050000},
		} {
			cfg := config.Config{Provider: test.provider, Model: modelID}
			model, _, err := resolveModelSettings(defaultProviders(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			display := contextDisplay(model, cfg, "system", nil, nil)
			if model.ContextWindow != test.window || display.Window != test.window || !display.Known {
				t.Fatalf("%s/%s: model window %d, display %+v", test.provider, modelID, model.ContextWindow, display)
			}
		}
	}
}
