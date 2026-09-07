package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

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
