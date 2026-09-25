package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/aihubmix"
)

func TestAiHubMixModelSelectionAndMenus(t *testing.T) {
	t.Parallel()
	providers := defaultProviders()
	configuration := config.Config{Provider: "aihubmix", AiHubMixAPIKey: "test-key"}
	model, options, err := resolveModelSettings(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "gpt-6-sol" || options.Thinking != llm.ThinkingLevelMedium {
		t.Fatalf("model/options = %#v/%#v", model, options)
	}
	service, err := modelForConfiguration(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.(*aihubmix.Provider); !ok {
		t.Fatalf("service = %T", service)
	}
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{saveSettings: recordSettings(func(config.Setting, string) error { return nil })}},
		model:       model, options: options, configuration: configuration, providers: providers,
	}
	login := runner.loginProviderMenu()
	found := false
	for _, option := range login.Options[1].Menu.Options {
		if option.Arguments == "aihubmix" {
			found = true
			if option.Menu == nil || !option.Menu.Options[0].UseSavedCredential {
				t.Fatal("missing saved AiHubMix credential selection")
			}
		}
	}
	if !found {
		t.Fatal("missing AiHubMix API-key login option")
	}
	if got := len(runner.modelMenu().Options); got != 7 {
		t.Fatalf("model count = %d", got)
	}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: "gpt-6-sol"}); err != nil {
		t.Fatal(err)
	}
	if got := len(runner.thinkingMenu().Options); got != 6 {
		t.Fatalf("GPT thinking choices = %d", got)
	}
	configuration.Model = "gpt-6-sol"
	configuration.ContextWindows = map[string]int64{"aihubmix/gpt-6-sol": 1048576}
	model, _, err = resolveModelSettings(providers, configuration)
	if err != nil || model.ContextWindow != 1048576 {
		t.Fatalf("GPT context = %d, error = %v", model.ContextWindow, err)
	}
}
