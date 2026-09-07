package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/zhipu"
)

func TestZhipuModelSelectionAndMenus(t *testing.T) {
	t.Parallel()
	providers := defaultProviders()
	configuration := config.Config{Provider: "zhipu", ZhipuAPIKey: "test-key"}
	model, options, err := resolveModelSettings(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "glm-5.3" || options.Thinking != llm.ThinkingLevelHigh {
		t.Fatalf("model/options = %#v/%#v", model, options)
	}
	service, err := modelForConfiguration(providers, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.(*zhipu.Provider); !ok {
		t.Fatalf("service = %T", service)
	}
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{saveSetting: func(config.Setting, string) error { return nil }}},
		model:       model, options: options, configuration: configuration, providers: providers,
	}
	login := runner.loginProviderMenu()
	found := false
	for _, option := range login.Options[1].Menu.Options {
		if option.Arguments == "zhipu" {
			found = true
			if option.Menu == nil || !option.Menu.Options[0].UseSavedCredential {
				t.Fatal("missing saved Zhipu credential selection")
			}
		}
	}
	if !found {
		t.Fatal("missing Zhipu API-key login option")
	}
	if got := len(runner.modelMenu().Options); got != 18 {
		t.Fatalf("model count = %d", got)
	}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: "glm-5.3-flash"}); err != nil {
		t.Fatal(err)
	}
	if runner.model.ID != "glm-5.3-flash" {
		t.Fatal("model selection did not switch to Flash")
	}
	if got := len(runner.thinkingMenu().Options); got != 3 {
		t.Fatalf("GLM-5.3 thinking choices = %d", got)
	}
	configuration.Model = "glm-5.3"
	configuration.ContextWindows = map[string]int64{"zhipu/glm-5.3": 1048576}
	model, _, err = resolveModelSettings(providers, configuration)
	if err != nil || model.ContextWindow != 1048576 {
		t.Fatalf("GLM-5.3 context = %d, error = %v", model.ContextWindow, err)
	}
}
