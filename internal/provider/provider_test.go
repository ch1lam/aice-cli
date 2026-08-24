package provider_test

import (
	"strings"
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
	for _, spec := range specs {
		deepSeekModel, ok := findModel(deepSeekModels, spec.ID)
		if !ok {
			t.Fatalf("DeepSeek catalog missing %q", spec.ID)
		}
		opencodeModel, ok := findModel(opencodeModels, spec.ID)
		if !ok {
			t.Fatalf("OpenCode Go catalog missing %q", spec.ID)
		}
		for name, pair := range map[string][2]any{
			"name":      {spec.Name, deepSeekModel.Name},
			"context":   {spec.ContextWindow, deepSeekModel.ContextWindow},
			"maxTokens": {spec.MaxTokens, deepSeekModel.MaxTokens},
		} {
			if pair[0] != pair[1] {
				t.Errorf("%s spec = %v, deepseek = %v", name, pair[0], pair[1])
			}
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

func TestValidateMessagesDeepSeekCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := provider.MessageCapabilities{
		ID:                       deepseek.ProviderID,
		Label:                    "DeepSeek",
		SupportsRedactedThinking: false,
		NestedToolResultTextOnly: true,
	}
	tests := []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{
			name: "image",
			part: llm.ContentPart{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			},
			want: "deepseek: message 0 content 0: image content is not supported by DeepSeek models",
		},
		{
			name: "redacted thinking",
			part: llm.ContentPart{
				Type:     llm.ContentTypeThinking,
				Text:     "opaque data",
				Redacted: true,
			},
			want: "deepseek: message 0 content 0: redacted thinking is not supported by DeepSeek models",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := provider.ValidateMessages([]llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{tt.part},
			}}, capabilities)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateMessages() error = %v, want %q", err, tt.want)
			}
		})
	}

	t.Run("nested tool result", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type: llm.ContentTypeToolResult,
				ToolResult: &llm.ToolResult{
					CallID:  "call-1",
					Content: []llm.ContentPart{{Type: llm.ContentTypeImage}},
				},
			}},
		}}, capabilities)
		want := "deepseek: message 0 content 0: non-text tool results are not supported by DeepSeek models"
		if err == nil || err.Error() != want {
			t.Fatalf("ValidateMessages() error = %v, want %q", err, want)
		}
	})

	t.Run("nil message", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{nil}, capabilities)
		if err == nil || err.Error() != "deepseek: message 0 is nil" {
			t.Fatalf("ValidateMessages() error = %v, want nil message error", err)
		}
	})
}

func TestValidateMessagesOpenCodeCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := provider.MessageCapabilities{
		ID:                       opencode.ProviderID,
		Label:                    "OpenCode Go",
		SupportsImage:            false,
		SupportsRedactedThinking: true,
		NestedToolResultTextOnly: false,
	}

	t.Run("image", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			}},
		}}, capabilities)
		want := "opencode-go: message 0 content 0: image content is not supported by OpenCode Go models"
		if err == nil || err.Error() != want {
			t.Fatalf("ValidateMessages() error = %v, want %q", err, want)
		}
	})

	t.Run("redacted thinking accepted", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type:     llm.ContentTypeThinking,
				Text:     "opaque data",
				Redacted: true,
			}},
		}}, capabilities)
		if err != nil {
			t.Fatalf("ValidateMessages() error = %v, want redacted thinking accepted", err)
		}
	})

	t.Run("nested tool result accepted", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type: llm.ContentTypeToolResult,
				ToolResult: &llm.ToolResult{
					CallID:  "call-1",
					Content: []llm.ContentPart{{Type: llm.ContentTypeImage}},
				},
			}},
		}}, capabilities)
		if err != nil {
			t.Fatalf("ValidateMessages() error = %v, want nested tool result accepted", err)
		}
	})

	t.Run("non-text tool result message", func(t *testing.T) {
		t.Parallel()

		err := provider.ValidateMessages([]llm.Message{llm.ToolResultMessage{
			Role: llm.RoleToolResult,
			Content: []llm.ContentPart{{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			}},
		}}, capabilities)
		want := "opencode-go: message 0 content 0: non-text tool results are not supported by OpenCode Go models"
		if err == nil || err.Error() != want {
			t.Fatalf("ValidateMessages() error = %v, want %q", err, want)
		}
	})
}

func TestProvidersSatisfyRegistrySurface(t *testing.T) {
	t.Parallel()

	registry := []provider.Provider{
		&deepseek.Provider{},
		&opencode.Provider{},
		&openai.Provider{},
	}
	if len(registry) != 3 {
		t.Fatalf("registry has %d providers, want 3", len(registry))
	}
	for _, candidate := range registry {
		if strings.TrimSpace(candidate.Label()) == "" {
			t.Errorf("%s label is empty", candidate.ProviderID())
		}
		if strings.TrimSpace(candidate.MenuDescription()) == "" {
			t.Errorf("%s menu description is empty", candidate.ProviderID())
		}
		if candidate.DefaultModel().Provider != candidate.ProviderID() {
			t.Errorf(
				"%s default model provider = %q, want %q",
				candidate.ProviderID(),
				candidate.DefaultModel().Provider,
				candidate.ProviderID(),
			)
		}
	}
}
