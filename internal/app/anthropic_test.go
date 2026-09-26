package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/anthropic"
)

func TestAnthropicModelSelectionAndMenus(t *testing.T) {
	t.Parallel()
	providers := defaultProviders()
	configuration := config.Config{Provider: "anthropic", AnthropicAPIKey: "test-key"}
	model, options, err := resolveModelSettings(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "claude-sonnet-5" || options.Thinking != llm.ThinkingLevelMedium {
		t.Fatalf("model/options = %#v/%#v", model, options)
	}
	service, err := modelForConfiguration(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.(*anthropic.Provider); !ok {
		t.Fatalf("service = %T", service)
	}
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{saveSettings: recordSettings(func(config.Setting, string) error { return nil })}},
		model:       model, options: options, configuration: configuration, providers: providers,
	}
	login := runner.loginProviderMenu()
	found := false
	for _, option := range login.Options[1].Menu.Options {
		if option.Arguments == "anthropic" {
			found = true
			if option.Menu == nil || !option.Menu.Options[0].UseSavedCredential {
				t.Fatal("missing saved Anthropic credential selection")
			}
		}
	}
	if !found {
		t.Fatal("missing Anthropic API-key login option")
	}
	if got := len(runner.modelMenu().Options); got != 4 {
		t.Fatalf("model count = %d", got)
	}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: "claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	if got := len(runner.thinkingMenu().Options); got != 6 {
		t.Fatalf("Claude thinking choices = %d", got)
	}
	configuration.Model = "claude-sonnet-5"
	configuration.ContextWindows = map[string]int64{"anthropic/claude-sonnet-5": 1048576}
	model, _, err = resolveModelSettings(providers, configuration)
	if err != nil || model.ContextWindow != 1048576 {
		t.Fatalf("Claude context = %d, error = %v", model.ContextWindow, err)
	}
}
