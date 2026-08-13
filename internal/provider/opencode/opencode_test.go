package opencode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestModels(t *testing.T) {
	t.Parallel()

	models := opencode.Models()
	if len(models) != 24 {
		t.Fatalf("Models() has %d entries, want 24", len(models))
	}
	for _, model := range models {
		if model.Provider != opencode.ProviderID {
			t.Errorf("model %q provider = %q, want %q", model.ID, model.Provider, opencode.ProviderID)
		}
		if model.API != openaicompletions.API {
			t.Errorf("model %q api = %q, want %q", model.ID, model.API, openaicompletions.API)
		}
		if !model.SupportsThinking {
			t.Errorf("model %q does not support thinking", model.ID)
		}
	}

	defaultModel := opencode.DefaultModel()
	if defaultModel.ID != "deepseek-v4-flash" {
		t.Errorf("DefaultModel() = %q, want %q", defaultModel.ID, "deepseek-v4-flash")
	}

	var kimi *llm.Model
	for index := range models {
		if models[index].ID == "kimi-k2.6" {
			kimi = &models[index]
			break
		}
	}
	if kimi == nil {
		t.Fatal("kimi-k2.6 missing from Models()")
	}
	if kimi.ContextWindow != 262_144 || kimi.MaxTokens != 65_536 {
		t.Errorf("kimi-k2.6 limits = %d/%d, want 262144/65536", kimi.ContextWindow, kimi.MaxTokens)
	}
	if kimi.Pricing.Input != 0.95 || kimi.Pricing.Output != 4 || kimi.Pricing.CacheRead != 0.16 {
		t.Errorf("kimi-k2.6 pricing = %#v", kimi.Pricing)
	}

	wantLevels := map[string][]llm.ThinkingLevel{
		// DeepSeek V4 exposes off, high, and max reasoning effort only.
		"deepseek-v4-flash": {llm.ThinkingLevelOff, llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		"deepseek-v4-pro":   {llm.ThinkingLevelOff, llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		// OpenCode Go exposes Kimi K2.6 thinking as on/off plus high only.
		"kimi-k2.6": {llm.ThinkingLevelOff, llm.ThinkingLevelHigh},
		// Kimi K3 always reasons at max.
		"kimi-k3": {llm.ThinkingLevelMax},
		// GLM-5.2 always reasons at high or max.
		"glm-5.2": {llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		// GPT-5.6 Luna excludes off and minimal.
		"gpt-5.6-luna": {
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
			llm.ThinkingLevelMax,
		},
		// Grok 4.5 exposes low, medium, and high efforts.
		"grok-4.5": {
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
		},
		// Hy3 maps off to none and exposes low and high.
		"hy3": {
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
		},
	}
	for modelID, want := range wantLevels {
		candidate, ok := modelForID(models, modelID)
		if !ok {
			t.Errorf("model %q missing from Models()", modelID)
			continue
		}
		if got := llm.SupportedThinkingLevels(candidate); !reflect.DeepEqual(got, want) {
			t.Errorf(
				"model %q supported thinking levels = %v, want %v",
				modelID,
				got,
				want,
			)
		}
	}

	clampTests := []struct {
		modelID   string
		request   llm.ThinkingLevel
		effective llm.ThinkingLevel
	}{
		{modelID: "kimi-k3", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelMax},
		{modelID: "gpt-5.6-luna", request: llm.ThinkingLevelOff, effective: llm.ThinkingLevelLow},
		{modelID: "grok-4.5", request: llm.ThinkingLevelMax, effective: llm.ThinkingLevelHigh},
		{modelID: "hy3", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelHigh},
	}
	for _, tt := range clampTests {
		model, ok := modelForID(models, tt.modelID)
		if !ok {
			t.Errorf("model %q missing from Models()", tt.modelID)
			continue
		}
		if got := llm.ClampThinkingLevel(model, tt.request); got != tt.effective {
			t.Errorf(
				"model %q clamps %q to %q, want %q",
				tt.modelID,
				tt.request,
				got,
				tt.effective,
			)
		}
	}

	hy3, ok := modelForID(models, "hy3")
	if !ok {
		t.Fatal("hy3 missing from Models()")
	}
	if got, supported := hy3.ThinkingLevelMap.WireValue(
		llm.ThinkingLevelOff,
	); !supported || got != "none" {
		t.Errorf("hy3 off wire value = %q/%v, want none/true", got, supported)
	}

	wantFormats := map[string]struct {
		format                  llm.ThinkingFormat
		supportsReasoningEffort bool
	}{
		"deepseek-v4-flash": {format: llm.ThinkingFormatDeepSeek},
		"deepseek-v4-pro":   {format: llm.ThinkingFormatDeepSeek},
		"kimi-k2.6":         {format: llm.ThinkingFormatDeepSeek},
		"qwen3.5-plus":      {format: llm.ThinkingFormatQwen},
		"qwen3.6-plus":      {format: llm.ThinkingFormatQwen},
	}
	for modelID, want := range wantFormats {
		candidate, ok := modelForID(models, modelID)
		if !ok {
			t.Errorf("model %q missing from Models()", modelID)
			continue
		}
		if candidate.ThinkingFormat != want.format ||
			candidate.SupportsReasoningEffort != want.supportsReasoningEffort {
			t.Errorf(
				"model %q thinking format = %q/%v, want %q/%v",
				modelID,
				candidate.ThinkingFormat,
				candidate.SupportsReasoningEffort,
				want.format,
				want.supportsReasoningEffort,
			)
		}
	}
}

func TestModelsReturnsIndependentThinkingMaps(t *testing.T) {
	t.Parallel()

	first := opencode.Models()
	if len(first) == 0 {
		t.Fatal("Models() returned no models")
	}
	first[0].ThinkingLevelMap[llm.ThinkingLevelOff] = llm.ThinkingValue("mutated")
	if value := first[0].ThinkingLevelMap[llm.ThinkingLevelHigh]; value != nil {
		*value = "mutated"
	}

	second := opencode.Models()
	if value, ok := second[0].ThinkingLevelMap.WireValue(
		llm.ThinkingLevelOff,
	); !ok || value != "off" {
		t.Errorf("second off wire value = %q/%v, want off/true", value, ok)
	}
	if value, ok := second[0].ThinkingLevelMap.WireValue(
		llm.ThinkingLevelHigh,
	); !ok || value != "high" {
		t.Errorf("second high wire value = %q/%v, want high/true", value, ok)
	}
}

func modelForID(models []llm.Model, id string) (llm.Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

func TestModelCatalogHasCompleteSpecs(t *testing.T) {
	t.Parallel()

	for _, model := range opencode.Models() {
		if model.Name == "" {
			t.Errorf("model %q has no display name", model.ID)
		}
		if model.ContextWindow <= 0 {
			t.Errorf("model %q has no context window", model.ID)
		}
		if model.MaxTokens <= 0 {
			t.Errorf("model %q has no max tokens", model.ID)
		}
		if model.Pricing.Input <= 0 || model.Pricing.Output <= 0 {
			t.Errorf("model %q has incomplete pricing", model.ID)
		}
	}
}

func TestProviderDispatchesThroughCompletionsAdapter(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider, err := opencode.New(opencode.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := llm.Request{
		Model: opencode.DefaultModel(),
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	}
	stream, err := provider.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := <-paths; got != "/chat/completions" {
		t.Errorf("request path = %q, want %q", got, "/chat/completions")
	}
}

func TestProviderRejectsModelAPIMismatchBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	provider, err := opencode.New(opencode.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := llm.Request{
		Model: opencode.DefaultModel(),
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	}
	request.Model.API = "anthropic-messages"
	_, err = provider.Stream(context.Background(), request)
	if err == nil ||
		!strings.Contains(err.Error(), `model "deepseek-v4-flash" API`) ||
		!strings.Contains(err.Error(), string(openaicompletions.API)) {
		t.Fatalf("Stream() error = %v, want model API mismatch", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestProviderRejectsUnsupportedContentBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	provider, err := opencode.New(opencode.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := llm.Request{
		Model: opencode.DefaultModel(),
		Messages: []llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			}},
		}},
	}
	_, err = provider.Stream(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "image content is not supported") {
		t.Fatalf("Stream() error = %v, want image rejection", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, err := opencode.New(opencode.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &opencode.Provider{}
	if got := descriptor.ProviderID(); got != opencode.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, opencode.ProviderID)
	}
	if got := descriptor.Label(); got != "OpenCode Go" {
		t.Errorf("Label() = %q, want OpenCode Go", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "OpenCode Go subscription") {
		t.Errorf("MenuDescription() = %q, want OpenCode Go subscription", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, opencode.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, opencode.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, opencode.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, opencode.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.OpenCodeAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), "AICE_OPENCODE_API_KEY") {
		t.Errorf("CredentialNotConfiguredError() = %v, want env var mention", err)
	}

	if _, err := descriptor.New(config.Config{}); err == nil {
		t.Error("New() error = nil, want missing API key error")
	}
}
