package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
)

func TestMoonshotModelSelectionAndMenus(t *testing.T) {
	t.Parallel()
	providers := defaultProviders()
	configuration := config.Config{Provider: "moonshot", MoonshotAPIKey: "test-key"}
	model, options, err := resolveModelSettings(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "kimi-k3" || options.Thinking != llm.ThinkingLevelHigh {
		t.Fatalf("model/options = %#v/%#v", model, options)
	}
	service, err := modelForConfiguration(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.(*moonshot.Provider); !ok {
		t.Fatalf("service = %T", service)
	}
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{saveSetting: func(config.Setting, string) error { return nil }}},
		model:       model, options: options, configuration: configuration, providers: providers,
	}
	login := runner.loginProviderMenu()
	found := false
	for _, option := range login.Options[1].Menu.Options {
		if option.Arguments == "moonshot" {
			found = true
			if option.Menu == nil || !option.Menu.Options[0].UseSavedCredential {
				t.Fatal("missing saved Moonshot credential selection")
			}
		}
	}
	if !found {
		t.Fatal("missing Moonshot API-key login option")
	}
	if got := len(runner.modelMenu().Options); got != 4 {
		t.Fatalf("model count = %d", got)
	}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: "kimi-k3"}); err != nil {
		t.Fatal(err)
	}
	if got := len(runner.thinkingMenu().Options); got != 3 {
		t.Fatalf("K3 thinking choices = %d", got)
	}
	configuration.Model = "kimi-k3"
	configuration.ContextWindows = map[string]int64{"moonshot/kimi-k3": 1048576}
	model, _, err = resolveModelSettings(providers, configuration)
	if err != nil || model.ContextWindow != 1048576 {
		t.Fatalf("K3 context = %d, error = %v", model.ContextWindow, err)
	}
}
