package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
)

// subagent_model picks a model of the main provider or, as provider/model,
// of another; subagent_thinking overrides the main thinking setting, and both
// clamp to the chosen model. Unset, children use the main model as is.
func TestSubagentModelResolution(t *testing.T) {
	t.Parallel()
	a := &application{dependencies: dependencies{providers: defaultProviders()}}
	main := func(cfg config.Config) configuredModel {
		cfg.Provider = string(deepseek.ProviderID)
		if cfg.APIKeys == nil {
			cfg.APIKeys = map[string]string{"deepseek": "key", "moonshot": "key"}
		}
		model, options, err := resolveModelSettings(a.dependencies.providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return configuredModel{configuration: cfg, model: model, options: options, service: &messagingModel{}}
	}
	for _, tt := range []struct {
		name     string
		cfg      config.Config
		provider llm.ProviderID
		model    string
		thinking llm.ThinkingLevel
		shared   bool
		err      string
	}{
		{name: "unset", cfg: config.Config{Thinking: llm.ThinkingLevelMax}, provider: deepseek.ProviderID, model: deepseek.ModelV4Flash, thinking: llm.ThinkingLevelMax, shared: true},
		{name: "same provider", cfg: config.Config{Thinking: llm.ThinkingLevelMax, SubagentModel: deepseek.ModelV4Pro}, provider: deepseek.ProviderID, model: deepseek.ModelV4Pro, thinking: llm.ThinkingLevelMax, shared: true},
		{name: "thinking only, clamped", cfg: config.Config{SubagentThinking: llm.ThinkingLevelMedium}, provider: deepseek.ProviderID, model: deepseek.ModelV4Flash, thinking: llm.ThinkingLevelHigh, shared: true},
		{name: "other provider", cfg: config.Config{SubagentModel: "moonshot/" + moonshot.ModelK3, SubagentThinking: llm.ThinkingLevelLow}, provider: moonshot.ProviderID, model: moonshot.ModelK3, thinking: llm.ClampThinkingLevel(mustModel(t, a, moonshot.ModelK3), llm.ThinkingLevelLow)},
		{name: "unknown model", cfg: config.Config{SubagentModel: "nope"}, err: "subagent_model"},
		{name: "provider without credential", cfg: config.Config{APIKeys: map[string]string{"deepseek": "key"}, SubagentModel: "moonshot/" + moonshot.ModelK3}, err: "subagent_model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configured := main(tt.cfg)
			child, err := resolveSubagentModel(a.dependencies.providers, configured)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if child.model.Provider != tt.provider || child.model.ID != tt.model || child.options.Thinking != tt.thinking || (child.service != nil) != tt.shared {
				t.Fatalf("child = %s/%s thinking %q, shared service %v", child.model.Provider, child.model.ID, child.options.Thinking, child.service != nil)
			}
		})
	}
}

func mustModel(t *testing.T, a *application, id string) llm.Model {
	t.Helper()
	model, ok := modelForProvider(a.dependencies.providers, string(moonshot.ProviderID), id)
	if !ok {
		t.Fatalf("no model %s", id)
	}
	return model
}

// A child runs on the subagents' model and thinking while the parent keeps
// its own; a subagent_model AICE cannot resolve fails the agent call, not
// the run.
func TestSubagentRunsOnItsModel(t *testing.T) {
	t.Parallel()
	var parents []llm.Request
	model := &messagingModel{
		child: func(_ context.Context, request llm.Request, _ int) (llm.Stream, error) {
			return textEventStream(request.Model, "child report", llm.Usage{}, llm.StopReasonStop), nil
		},
		parent: func(m *messagingModel, request llm.Request, last llm.AgentMessage) llm.Stream {
			parents = append(parents, request)
			if result, ok := last.(llm.ToolResultMessage); ok {
				return textEventStream(request.Model, "parent saw: "+result.Content[0].Text, llm.Usage{}, llm.StopReasonStop)
			}
			return m.call(request, "probe", "agent", map[string]any{"description": "probe", "prompt": "look", "type": "explore", "run_in_background": false})
		},
	}
	cfg := config.Config{APIKeys: map[string]string{"deepseek": "test-key"}, Thinking: llm.ThinkingLevelMax, SubagentModel: deepseek.ModelV4Pro, SubagentThinking: llm.ThinkingLevelLow}
	if output := runPrintWithConfig(t, cfg, model); !strings.Contains(output, "child report") {
		t.Fatalf("output = %q", output)
	}
	if len(model.children) != 1 || model.children[0].Model.ID != deepseek.ModelV4Pro || model.children[0].Options.Thinking != llm.ThinkingLevelLow {
		t.Fatalf("child request = %s thinking %q", model.children[0].Model.ID, model.children[0].Options.Thinking)
	}
	if parents[0].Model.ID != deepseek.ModelV4Flash || parents[0].Options.Thinking != llm.ThinkingLevelMax {
		t.Fatalf("parent request = %s thinking %q", parents[0].Model.ID, parents[0].Options.Thinking)
	}

	parents = nil
	model.children = nil
	cfg.SubagentModel = "nope"
	if output := runPrintWithConfig(t, cfg, model); !strings.Contains(output, "parent saw:") || !strings.Contains(output, "subagent_model") {
		t.Fatalf("output = %q", output)
	}
	if len(model.children) != 0 {
		t.Fatalf("a child ran on an unresolved model")
	}
}

// The settings view shows the subagents' effective model and thinking, with
// every provider's models to choose from.
func TestSettingsShowSubagentModel(t *testing.T) {
	t.Parallel()
	c := config.Config{Provider: "deepseek", APIKeys: map[string]string{"deepseek": "key", "moonshot": "key"}, SubagentModel: "moonshot/" + moonshot.ModelK3}
	model, options, err := resolveModelSettings(defaultProviders(), c)
	if err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{configuration: c, model: model, options: options, providers: defaultProviders()}
	snapshot, err := s.ReadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]interaction.SettingField{}
	for _, field := range snapshot.Fields {
		fields[field.ID] = field
	}
	sub := fields["subagent_model"]
	if sub.Effective != "moonshot/"+moonshot.ModelK3 || !sub.AllowCustom ||
		!slices.ContainsFunc(sub.Choices, func(c interaction.SettingChoice) bool { return c.Value == "deepseek/"+deepseek.ModelV4Pro }) {
		t.Fatalf("subagent_model field = %#v", sub)
	}
	if thinking := fields["subagent_thinking"]; thinking.Effective == "" || len(thinking.Choices) == 0 {
		t.Fatalf("subagent_thinking field = %#v", thinking)
	}
}
