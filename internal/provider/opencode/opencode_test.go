package opencode_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func modelForID(models []llm.Model, id string) (llm.Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

func TestProviderDispatchesModelsThroughConfiguredAPIs(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.Header.Get("x-opencode-session") == "" {
			http.Error(w, `{"type":"MissingSessionID"}`, http.StatusBadRequest)
			return
		}
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
		"deepseek-v4.1-flash",
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

// The gateway accepts thinking {"type":"disabled"} for these models, which
// lets fast callers such as the auto mode classifier skip reasoning.
func TestDeepSeekModelsCanDisableThinking(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"deepseek-v4.1-flash", "deepseek-v4-pro"} {
		found := false
		for _, model := range opencode.Models() {
			if model.ID != id {
				continue
			}
			found = true
			if llm.ClampThinkingLevel(model, llm.ThinkingLevelOff) != llm.ThinkingLevelOff {
				t.Errorf("%s does not expose thinking off: %v", id, llm.SupportedThinkingLevels(model))
			}
		}
		if !found {
			t.Errorf("model %s is missing", id)
		}
	}
}

func TestCalibratedThinkingControlsOnWire(t *testing.T) {
	t.Parallel()

	capture := func(t *testing.T, modelID string, level llm.ThinkingLevel) map[string]any {
		t.Helper()
		model, ok := modelForID(opencode.Models(), modelID)
		if !ok {
			t.Fatalf("model %q missing", modelID)
		}
		bodies := make(chan map[string]any, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			bodies <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "")
		}))
		defer server.Close()

		p, err := opencode.New(opencode.Config{
			APIKey:     "test-key",
			BaseURL:    server.URL + "/v1",
			HTTPClient: server.Client(),
		})
		if err != nil {
			t.Fatal(err)
		}
		stream, err := p.Stream(t.Context(), llm.Request{
			Model:   model,
			Options: llm.StreamOptions{Thinking: level},
			Messages: []llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			}},
		})
		if err != nil {
			t.Fatalf("Stream(%s, %s) error = %v", modelID, level, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		return <-bodies
	}

	t.Run("claude-haiku-5-5", func(t *testing.T) {
		t.Parallel()
		model, _ := modelForID(opencode.Models(), "claude-haiku-5-5")
		want := []llm.ThinkingLevel{
			llm.ThinkingLevelOff, llm.ThinkingLevelLow, llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax,
		}
		if got := llm.SupportedThinkingLevels(model); !slices.Equal(got, want) {
			t.Fatalf("levels = %v, want %v", got, want)
		}
		offBody := capture(t, "claude-haiku-5-5", llm.ThinkingLevelOff)
		if thinking, _ := offBody["thinking"].(map[string]any); thinking["type"] != "disabled" {
			t.Errorf("off thinking = %#v", offBody["thinking"])
		}
		if _, hasOutput := offBody["output_config"]; hasOutput {
			t.Errorf("off output_config = %#v, want omitted", offBody["output_config"])
		}
		highBody := capture(t, "claude-haiku-5-5", llm.ThinkingLevelHigh)
		if thinking, _ := highBody["thinking"].(map[string]any); thinking["type"] != "adaptive" {
			t.Errorf("high thinking = %#v", highBody["thinking"])
		}
		if cfg, _ := highBody["output_config"].(map[string]any); cfg["effort"] != "high" {
			t.Errorf("high output_config = %#v", highBody["output_config"])
		}
	})

	t.Run("longcat-2.5", func(t *testing.T) {
		t.Parallel()
		model, _ := modelForID(opencode.Models(), "longcat-2.5")
		want := []llm.ThinkingLevel{llm.ThinkingLevelOff, llm.ThinkingLevelHigh}
		if got := llm.SupportedThinkingLevels(model); !slices.Equal(got, want) {
			t.Fatalf("levels = %v, want %v", got, want)
		}
		for _, tc := range []struct {
			level llm.ThinkingLevel
			want  string
		}{
			{llm.ThinkingLevelOff, "disabled"},
			{llm.ThinkingLevelHigh, "enabled"},
		} {
			body := capture(t, "longcat-2.5", tc.level)
			if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != tc.want {
				t.Errorf("%s thinking = %#v, want %q", tc.level, body["thinking"], tc.want)
			}
			if _, hasEffort := body["reasoning_effort"]; hasEffort {
				t.Errorf("%s unexpected reasoning_effort = %#v", tc.level, body["reasoning_effort"])
			}
		}
	})

	t.Run("kimi-k2.7-code", func(t *testing.T) {
		t.Parallel()
		model, _ := modelForID(opencode.Models(), "kimi-k2.7-code")
		want := []llm.ThinkingLevel{llm.ThinkingLevelHigh}
		if got := llm.SupportedThinkingLevels(model); !slices.Equal(got, want) {
			t.Fatalf("levels = %v, want %v", got, want)
		}
		body := capture(t, "kimi-k2.7-code", llm.ThinkingLevelHigh)
		if effort, ok := body["reasoning_effort"]; ok && strings.TrimSpace(effort.(string)) != "" {
			t.Errorf("unexpected reasoning_effort = %#v", effort)
		}
		if _, hasThinking := body["thinking"]; hasThinking {
			t.Errorf("unexpected thinking = %#v", body["thinking"])
		}
	})
}
