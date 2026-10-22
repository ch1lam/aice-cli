package provider_test

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestBuiltinCatalogsDeclareExplicitThinkingLevelMaps(t *testing.T) {
	t.Parallel()

	catalogs := []struct {
		name   string
		models []llm.Model
	}{
		{name: string(deepseek.ProviderID), models: deepseek.Models()},
		{name: string(opencode.ProviderID), models: opencode.Models()},
		{name: string(openai.ProviderID), models: openai.Models()},
	}
	for _, catalog := range catalogs {
		for _, model := range catalog.models {
			if model.ThinkingLevelMap == nil {
				t.Errorf(
					"%s model %q has no explicit thinking level map",
					catalog.name,
					model.ID,
				)
				continue
			}
			for _, level := range []llm.ThinkingLevel{
				llm.ThinkingLevelOff,
				llm.ThinkingLevelMinimal,
				llm.ThinkingLevelLow,
				llm.ThinkingLevelMedium,
				llm.ThinkingLevelHigh,
				llm.ThinkingLevelXHigh,
				llm.ThinkingLevelMax,
			} {
				if _, exists := model.ThinkingLevelMap[level]; !exists {
					t.Errorf(
						"%s model %q thinking level map is missing the %q key",
						catalog.name,
						model.ID,
						level,
					)
				}
			}
		}
	}
}
