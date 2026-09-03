package opencode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestModels(t *testing.T) {
	t.Parallel()

	models := opencode.Models()
	wantIDs := []string{
		"grok-4.6",
		"gpt-5.6-luna",
		"glm-5.3-flash",
		"glm-5.3",
		"glm-5.2",
		"glm-5.1",
		"kimi-k3",
		"kimi-k2.7-code",
		"kimi-k2.6",
		"longcat-2.0",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"deepseek-v4-flash-vision-exp",
		"mimo-v2.5",
		"mimo-v2.5-pro",
		"minimax-m3",
		"minimax-m2.7",
		"muse-spark-1.3-contributor",
		"muse-spark-1.2-contributor",
		"qwen3.8-max",
		"qwen3.8-flash",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.6-plus",
		"hy4-preview",
		"hy3",
	}
	gotIDs := make([]string, 0, len(models))
	for _, model := range models {
		gotIDs = append(gotIDs, model.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("Models() IDs = %v, want %v", gotIDs, wantIDs)
	}
	responsesModels := map[string]bool{
		"gpt-5.6-luna":               true,
		"grok-4.6":                   true,
		"muse-spark-1.2-contributor": true,
		"muse-spark-1.3-contributor": true,
	}
	anthropicModels := map[string]bool{
		"minimax-m2.7":  true,
		"minimax-m3":    true,
		"qwen3.6-plus":  true,
		"qwen3.7-max":   true,
		"qwen3.7-plus":  true,
		"qwen3.8-max":   true,
		"qwen3.8-flash": true,
	}
	for _, model := range models {
		if model.Provider != opencode.ProviderID {
			t.Errorf("model %q provider = %q, want %q", model.ID, model.Provider, opencode.ProviderID)
		}
		wantAPI := openaicompletions.API
		if responsesModels[model.ID] {
			wantAPI = openairesponses.API
		} else if anthropicModels[model.ID] {
			wantAPI = anthropic.API
		}
		if model.API != wantAPI {
			t.Errorf("model %q api = %q, want %q", model.ID, model.API, wantAPI)
		}
		if !model.OmitMaxTokensByDefault {
			t.Errorf("model %q does not omit default max tokens", model.ID)
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

	vision, ok := modelForID(models, "deepseek-v4-flash-vision-exp")
	if !ok {
		t.Fatal("deepseek-v4-flash-vision-exp missing from Models()")
	}
	wantModalities := []llm.InputModality{llm.InputModalityText, llm.InputModalityImage}
	if !reflect.DeepEqual(vision.InputModalities, wantModalities) {
		t.Errorf("vision modalities = %v, want %v", vision.InputModalities, wantModalities)
	}

	textOnly, ok := modelForID(models, "glm-5.3")
	if !ok {
		t.Fatal("glm-5.3 missing from Models()")
	}
	if want := []llm.InputModality{llm.InputModalityText}; !reflect.DeepEqual(textOnly.InputModalities, want) {
		t.Errorf("glm-5.3 modalities = %v, want %v", textOnly.InputModalities, want)
	}

	gpt, ok := modelForID(models, "gpt-5.6-luna")
	if !ok {
		t.Fatal("gpt-5.6-luna missing from Models()")
	}
	if gpt.Pricing.CacheWrite != 0.25 {
		t.Errorf("gpt-5.6-luna cache-write pricing = %v, want 0.25", gpt.Pricing.CacheWrite)
	}

	wantLevels := map[string][]llm.ThinkingLevel{
		// DeepSeek V4 Flash exposes low, high, and max effort.
		"deepseek-v4-flash": {
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		},
		// DeepSeek V4 Pro exposes high and max effort.
		"deepseek-v4-pro": {
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		},
		// The vision experiment also exposes a thinking toggle.
		"deepseek-v4-flash-vision-exp": {
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		},
		// OpenCode Go exposes Kimi K2.6 thinking as on/off plus high only.
		"kimi-k2.6": {llm.ThinkingLevelOff, llm.ThinkingLevelHigh},
		// Kimi K3 always reasons at max.
		"kimi-k3": {llm.ThinkingLevelMax},
		// GLM-5.2 always reasons at high or max.
		"glm-5.2": {llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		// GLM-5.3 exposes low, high, and max.
		"glm-5.3": {llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		// GLM-5.3-Flash exposes low, high, and max.
		"glm-5.3-flash": {llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax},
		// GPT-5.6 Luna maps off to none and excludes minimal.
		"gpt-5.6-luna": {
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
			llm.ThinkingLevelMax,
		},
		// Grok 4.6 exposes low, medium, high, and xhigh efforts.
		"grok-4.6": {
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		},
		// Muse exposes minimal through xhigh.
		"muse-spark-1.2-contributor": {
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		},
		"muse-spark-1.3-contributor": {
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		},
		// Hy3 maps off to none and exposes low and high.
		"hy3": {
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
		},
		// Hy4 preview maps off to none and exposes high.
		"hy4-preview": {
			llm.ThinkingLevelOff,
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
		{modelID: "deepseek-v4-flash", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelHigh},
		{modelID: "deepseek-v4-flash", request: llm.ThinkingLevelXHigh, effective: llm.ThinkingLevelMax},
		{modelID: "deepseek-v4-pro", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelHigh},
		{modelID: "deepseek-v4-pro", request: llm.ThinkingLevelXHigh, effective: llm.ThinkingLevelMax},
		{modelID: "kimi-k3", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelMax},
		{modelID: "gpt-5.6-luna", request: llm.ThinkingLevelMinimal, effective: llm.ThinkingLevelLow},
		{modelID: "grok-4.6", request: llm.ThinkingLevelMax, effective: llm.ThinkingLevelXHigh},
		{modelID: "hy3", request: llm.ThinkingLevelMedium, effective: llm.ThinkingLevelHigh},
		{modelID: "hy4-preview", request: llm.ThinkingLevelLow, effective: llm.ThinkingLevelHigh},
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
	if got, supported := gpt.ThinkingLevelMap.WireValue(
		llm.ThinkingLevelOff,
	); !supported || got != "none" {
		t.Errorf("gpt-5.6-luna off wire value = %q/%v, want none/true", got, supported)
	}

	wantFormats := map[string]struct {
		format                  llm.ThinkingFormat
		supportsReasoningEffort bool
	}{
		"deepseek-v4-flash": {format: llm.ThinkingFormatDeepSeek, supportsReasoningEffort: true},
		"deepseek-v4-pro":   {format: llm.ThinkingFormatDeepSeek, supportsReasoningEffort: true},
		"deepseek-v4-flash-vision-exp": {
			format: llm.ThinkingFormatDeepSeek, supportsReasoningEffort: true,
		},
		"kimi-k2.6": {format: llm.ThinkingFormatDeepSeek},
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
	firstVision, ok := modelForID(first, "deepseek-v4-flash-vision-exp")
	if !ok {
		t.Fatal("deepseek-v4-flash-vision-exp missing from Models()")
	}
	firstVision.ThinkingLevelMap[llm.ThinkingLevelOff] = llm.ThinkingValue("mutated")
	if value := firstVision.ThinkingLevelMap[llm.ThinkingLevelHigh]; value != nil {
		*value = "mutated"
	}

	second := opencode.Models()
	secondVision, ok := modelForID(second, "deepseek-v4-flash-vision-exp")
	if !ok {
		t.Fatal("deepseek-v4-flash-vision-exp missing from second Models()")
	}
	if value, supported := secondVision.ThinkingLevelMap.WireValue(
		llm.ThinkingLevelOff,
	); !supported || value != "off" {
		t.Errorf("second off wire value = %q/%v, want off/true", value, supported)
	}
	if value, supported := secondVision.ThinkingLevelMap.WireValue(
		llm.ThinkingLevelHigh,
	); !supported || value != "high" {
		t.Errorf("second high wire value = %q/%v, want high/true", value, supported)
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

func TestProviderDispatchesModelsThroughConfiguredAPIs(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	provider, err := opencode.New(opencode.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/gateway/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, modelID := range []string{
		"deepseek-v4-flash",
		"gpt-5.6-luna",
		"qwen3.8-max",
	} {
		model, ok := modelForID(opencode.Models(), modelID)
		if !ok {
			t.Fatalf("model %q missing from Models()", modelID)
		}
		request := llm.Request{
			Model: model,
			Messages: []llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			}},
		}
		stream, err := provider.Stream(context.Background(), request)
		if err != nil {
			t.Fatalf("Stream(%q) error = %v", modelID, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close(%q) error = %v", modelID, err)
		}
	}

	got := []string{<-paths, <-paths, <-paths}
	want := []string{
		"/gateway/v1/chat/completions",
		"/gateway/v1/responses",
		"/gateway/v1/messages",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request paths = %v, want %v", got, want)
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

func TestProviderAcceptsImageForVisionModel(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
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
	model, ok := modelForID(opencode.Models(), "deepseek-v4-flash-vision-exp")
	if !ok {
		t.Fatal("deepseek-v4-flash-vision-exp missing from Models()")
	}
	request := llm.Request{
		Model: model,
		Messages: []llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			}},
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
	if got := descriptor.MenuDescription(); !strings.Contains(got, "26 models") {
		t.Errorf("MenuDescription() = %q, want 26 models", got)
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
