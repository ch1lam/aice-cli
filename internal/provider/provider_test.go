package provider_test

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestDeepSeekModelSpecsSeedCompatibleCatalogs(t *testing.T) {
	t.Parallel()

	specs := provider.DeepSeekModelSpecs()
	if len(specs) != 2 {
		t.Fatalf("DeepSeekModelSpecs() has %d entries, want 2", len(specs))
	}
	deepSeekModels := deepseek.Models()
	opencodeModels := opencode.Models()
	// OpenCode Go retired the deepseek-v4-flash ID and serves V4.1 Flash
	// instead, so only the shared Pro specification seeds that catalog.
	for _, spec := range specs {
		deepSeekID, deepSeekName := spec.ID, spec.Name
		if spec.ID == "deepseek-v4-flash" {
			deepSeekID, deepSeekName = "deepseek-flash", "DeepSeek Flash"
		}
		deepSeekModel, ok := findModel(deepSeekModels, deepSeekID)
		if !ok {
			t.Fatalf("DeepSeek catalog missing %q", spec.ID)
		}
		for name, pair := range map[string][2]any{
			"name":      {deepSeekName, deepSeekModel.Name},
			"context":   {spec.ContextWindow, deepSeekModel.ContextWindow},
			"maxTokens": {spec.MaxTokens, deepSeekModel.MaxTokens},
		} {
			if pair[0] != pair[1] {
				t.Errorf("%s spec = %v, deepseek = %v", name, pair[0], pair[1])
			}
		}
		for name, want := range map[string]float64{
			"input":     spec.Input,
			"output":    spec.Output,
			"cacheRead": spec.CacheRead,
		} {
			if pair := map[string]float64{
				"input":     deepSeekModel.Pricing.Input,
				"output":    deepSeekModel.Pricing.Output,
				"cacheRead": deepSeekModel.Pricing.CacheRead,
			}; pair[name] != want {
				t.Errorf("deepseek %s %s pricing = %v, want %v", spec.ID, name, pair[name], want)
			}
		}
		if spec.ID != "deepseek-v4-pro" {
			continue
		}
		opencodeModel, ok := findModel(opencodeModels, spec.ID)
		if !ok {
			t.Fatalf("OpenCode Go catalog missing %q", spec.ID)
		}
		if opencodeModel.ContextWindow != spec.ContextWindow ||
			opencodeModel.MaxTokens != spec.MaxTokens {
			t.Errorf(
				"opencode %s limits = %d/%d, want %d/%d",
				spec.ID,
				opencodeModel.ContextWindow,
				opencodeModel.MaxTokens,
				spec.ContextWindow,
				spec.MaxTokens,
			)
		}
	}
}

func TestDeepSeekModelSpecsReturnsIndependentThinkingMaps(t *testing.T) {
	t.Parallel()

	first := provider.DeepSeekModelSpecs()
	if len(first) == 0 {
		t.Fatal("DeepSeekModelSpecs() returned no models")
	}
	first[0].ThinkingLevelMap[llm.ThinkingLevelOff] = llm.ThinkingValue("mutated")
	if value := first[0].ThinkingLevelMap[llm.ThinkingLevelHigh]; value != nil {
		*value = "mutated"
	}

	second := provider.DeepSeekModelSpecs()
	if value, ok := second[0].ThinkingLevelMap.WireValue(
		llm.ThinkingLevelOff,
	); !ok || value != "off" {
		t.Errorf("second off wire value = %q/%v, want off/true", value, ok)
	}
	for _, tt := range []struct {
		level llm.ThinkingLevel
		want  string
	}{
		{level: llm.ThinkingLevelLow, want: "low"},
		{level: llm.ThinkingLevelMedium, want: "high"},
		{level: llm.ThinkingLevelHigh, want: "high"},
		{level: llm.ThinkingLevelXHigh, want: "high"},
		{level: llm.ThinkingLevelMax, want: "max"},
	} {
		if value, ok := second[0].ThinkingLevelMap.WireValue(tt.level); !ok || value != tt.want {
			t.Errorf(
				"second %s wire value = %q/%v, want %s/true",
				tt.level,
				value,
				ok,
				tt.want,
			)
		}
	}
}

func findModel(models []llm.Model, id string) (llm.Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

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
