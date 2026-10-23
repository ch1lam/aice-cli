package app

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/aihubmix"
	"github.com/ch1lam/aice-cli/internal/provider/anthropic"
	"github.com/ch1lam/aice-cli/internal/provider/kimi"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
	"github.com/ch1lam/aice-cli/internal/provider/zhipu"
)

// Each API-key provider resolves its default model and service, offers a saved
// credential in /login, lists its models, switches model through /model and
// honors a configured context window.
func TestAPIKeyProviderModelSelectionAndMenus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		provider        string
		service         any
		defaultModel    string
		defaultThinking llm.ThinkingLevel
		models          int
		selectModel     string
		thinkingChoices int
		contextModel    string
	}{
		{provider: "zhipu", service: (*zhipu.Provider)(nil), defaultModel: "glm-5.3", defaultThinking: llm.ThinkingLevelHigh, models: 18, selectModel: "glm-5.3-flash", thinkingChoices: 3, contextModel: "glm-5.3"},
		{provider: "zhipu-coding", service: (*zhipu.Provider)(nil), defaultModel: "glm-5.3", defaultThinking: llm.ThinkingLevelHigh, models: 2, selectModel: "glm-5.3-flash", thinkingChoices: 3, contextModel: "glm-5.3"},
		{provider: "kimi-coding", service: (*kimi.Provider)(nil), defaultModel: "kimi-for-coding", defaultThinking: llm.ThinkingLevelHigh, models: 4, selectModel: "k3-256k", thinkingChoices: 3, contextModel: "k3"},
		{provider: "moonshot", service: (*moonshot.Provider)(nil), defaultModel: "kimi-k3", defaultThinking: llm.ThinkingLevelHigh, models: 4, selectModel: "kimi-k3", thinkingChoices: 3, contextModel: "kimi-k3"},
		{provider: "anthropic", service: (*anthropic.Provider)(nil), defaultModel: "claude-sonnet-5", defaultThinking: llm.ThinkingLevelMedium, models: 4, selectModel: "claude-sonnet-5", thinkingChoices: 6, contextModel: "claude-sonnet-5"},
		{provider: "aihubmix", service: (*aihubmix.Provider)(nil), defaultModel: "gpt-6-sol", defaultThinking: llm.ThinkingLevelMedium, models: 7, selectModel: "gpt-6-sol", thinkingChoices: 6, contextModel: "gpt-6-sol"},
	} {
		t.Run(test.provider, func(t *testing.T) {
			t.Parallel()
			providers := defaultProviders()
			configuration := config.Config{Provider: test.provider, APIKeys: map[string]string{test.provider: "test-key"}}
			model, options, err := resolveModelSettings(providers, configuration)
			if err != nil {
				t.Fatal(err)
			}
			if model.ID != test.defaultModel || options.Thinking != test.defaultThinking {
				t.Fatalf("model/options = %#v/%#v", model, options)
			}
			service, err := modelForConfiguration(providers, configuration)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := reflect.TypeOf(service), reflect.TypeOf(test.service); got != want {
				t.Fatalf("service = %v, want %v", got, want)
			}
			runner := &interactiveSession{
				application: &application{dependencies: dependencies{saveSettings: recordSettings(func(config.Setting, string) error { return nil })}},
				model:       model, options: options, configuration: configuration, providers: providers,
			}
			login := runner.loginProviderMenu()
			found := false
			for _, option := range login.Options[1].Menu.Options {
				if option.Arguments == test.provider {
					found = true
					if option.Menu == nil || !option.Menu.Options[0].UseSavedCredential {
						t.Fatal("missing saved credential selection")
					}
				}
			}
			if !found {
				t.Fatal("missing API-key login option")
			}
			if got := len(runner.modelMenu().Options); got != test.models {
				t.Fatalf("model count = %d, want %d", got, test.models)
			}
			if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: test.selectModel}); err != nil {
				t.Fatal(err)
			}
			if runner.model.ID != test.selectModel {
				t.Fatalf("selected model = %q, want %q", runner.model.ID, test.selectModel)
			}
			if got := len(runner.thinkingMenu().Options); got != test.thinkingChoices {
				t.Fatalf("thinking choices = %d, want %d", got, test.thinkingChoices)
			}
			configuration.Model = test.contextModel
			configuration.ContextWindows = map[string]int64{test.provider + "/" + test.contextModel: 1048576}
			model, _, err = resolveModelSettings(providers, configuration)
			if err != nil || model.ContextWindow != 1048576 {
				t.Fatalf("context = %d, error = %v", model.ContextWindow, err)
			}
		})
	}
}
