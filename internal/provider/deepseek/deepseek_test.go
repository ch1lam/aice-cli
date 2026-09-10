package deepseek_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestModels(t *testing.T) {
	t.Parallel()

	want := []llm.Model{
		{
			ID:               deepseek.ModelV4Flash,
			Name:             "DeepSeek Flash",
			API:              openairesponses.API,
			Provider:         deepseek.ProviderID,
			SupportsThinking: true,
			ThinkingLevelMap: deepSeekThinkingLevelMap(),
			InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			ContextWindow:    1_000_000,
			MaxTokens:        384_000,
			Pricing: llm.Pricing{
				Input:     0.22,
				Output:    0.66,
				CacheRead: 0.007,
			},
		},
		{
			ID:                      deepseek.ModelV4Pro,
			Name:                    "DeepSeek V4 Pro",
			API:                     anthropic.API,
			Provider:                deepseek.ProviderID,
			SupportsThinking:        true,
			ThinkingLevelMap:        deepSeekThinkingLevelMap(),
			SupportsReasoningEffort: true,
			InputModalities:         []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			ContextWindow:           1_000_000,
			MaxTokens:               384_000,
			Pricing: llm.Pricing{
				Input:     0.66,
				Output:    1.98,
				CacheRead: 0.022,
			},
		},
	}
	models := deepseek.Models()
	if !reflect.DeepEqual(models, want) {
		t.Errorf("Models() = %#v, want %#v", models, want)
	}
	wantChoices := []llm.ThinkingLevel{
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelMax,
	}
	for _, model := range models {
		if got := llm.SupportedThinkingLevels(model); !reflect.DeepEqual(got, wantChoices) {
			t.Errorf("model %q thinking choices = %v, want %v", model.ID, got, wantChoices)
		}
	}
	if got := deepseek.DefaultModel(); !reflect.DeepEqual(got, want[0]) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, want[0])
	}
}

func deepSeekThinkingLevelMap() llm.ThinkingLevelMap {
	return llm.ThinkingLevelMap{
		llm.ThinkingLevelOff:     llm.ThinkingValue("off"),
		llm.ThinkingLevelMinimal: nil,
		llm.ThinkingLevelLow:     llm.ThinkingValue("low"),
		llm.ThinkingLevelMedium:  llm.ThinkingValue("high"),
		llm.ThinkingLevelHigh:    llm.ThinkingValue("high"),
		llm.ThinkingLevelXHigh:   llm.ThinkingValue("high"),
		llm.ThinkingLevelMax:     llm.ThinkingValue("max"),
	}
}

func TestProviderDispatchesEachModelThroughItsConfiguredAPI(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	provider, err := deepseek.New(deepseek.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, model := range deepseek.Models() {
		request := llm.Request{
			Model: model,
			Messages: []llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			}},
		}
		stream, err := provider.Stream(context.Background(), request)
		if err != nil {
			t.Fatalf("Stream(%q) error = %v", model.ID, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close(%q) error = %v", model.ID, err)
		}
	}

	got := []string{<-paths, <-paths}
	want := []string{"/responses", "/anthropic/v1/messages"}
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

	provider, err := deepseek.New(deepseek.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := llm.Request{
		Model: deepseek.DefaultModel(),
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	}
	request.Model.API = anthropic.API
	_, err = provider.Stream(context.Background(), request)
	if err == nil ||
		!strings.Contains(err.Error(), `model "deepseek-flash" API`) ||
		!strings.Contains(err.Error(), string(openairesponses.API)) {
		t.Fatalf("Stream() error = %v, want model API mismatch", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestProviderRejectsUnsupportedDeepSeekContentBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	provider, err := deepseek.New(deepseek.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{
			name: "redacted thinking",
			part: llm.ContentPart{
				Type:     llm.ContentTypeThinking,
				Text:     "opaque data",
				Redacted: true,
			},
			want: "redacted thinking is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := llm.Request{
				Model: deepseek.DefaultModel(),
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{tt.part},
				}},
			}
			_, err := provider.Stream(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream() error = %v, want text %q", err, tt.want)
			}
		})
	}

	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, err := deepseek.New(deepseek.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &deepseek.Provider{}
	if got := descriptor.ProviderID(); got != deepseek.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, deepseek.ProviderID)
	}
	if got := descriptor.Label(); got != "DeepSeek" {
		t.Errorf("Label() = %q, want DeepSeek", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "DeepSeek API") {
		t.Errorf("MenuDescription() = %q, want DeepSeek API", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, deepseek.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, deepseek.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, deepseek.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, deepseek.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.DeepSeekAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), "AICE_DEEPSEEK_API_KEY") {
		t.Errorf("CredentialNotConfiguredError() = %v, want env var mention", err)
	}

	if _, err := descriptor.New(config.Config{}); err == nil {
		t.Error("New() error = nil, want missing API key error")
	}
}

func TestVisionModelsSendImageAndExactModelID(t *testing.T) {
	t.Parallel()
	for _, modelID := range []string{deepseek.ModelV4Flash, deepseek.ModelV4Pro} {
		t.Run(modelID, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				bodies <- string(body)
				wantPath := "/responses"
				if modelID == deepseek.ModelV4Pro {
					wantPath = "/anthropic/v1/messages"
				}
				if r.URL.Path != wantPath {
					t.Errorf("path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			service, err := deepseek.New(deepseek.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			var selected llm.Model
			for _, model := range deepseek.Models() {
				if model.ID == modelID {
					selected = model
				}
			}
			stream, err := service.Stream(t.Context(), llm.Request{
				Model:   selected,
				Options: llm.StreamOptions{Thinking: llm.ThinkingLevelHigh},
				Messages: []llm.Message{llm.UserMessage{
					Role: llm.RoleUser,
					Content: []llm.ContentPart{
						llm.NewTextContent("Describe this image").Part(),
						{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}},
					},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			body := <-bodies
			var payload struct {
				Model     string `json:"model"`
				Reasoning struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
				OutputConfig struct {
					Effort string `json:"effort"`
				} `json:"output_config"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Model != modelID {
				t.Errorf("model = %q", payload.Model)
			}
			effort := payload.Reasoning.Effort
			imageType, imageData := `"type":"input_image"`, "data:image/png;base64,aW1hZ2U="
			if modelID == deepseek.ModelV4Pro {
				effort = payload.OutputConfig.Effort
				imageType, imageData = `"type":"image"`, `"data":"aW1hZ2U="`
			}
			if effort != "high" {
				t.Errorf("effort = %q", effort)
			}
			if !strings.Contains(body, imageType) || !strings.Contains(body, imageData) {
				t.Errorf("image missing from request: %s", body)
			}

		})
	}
}
