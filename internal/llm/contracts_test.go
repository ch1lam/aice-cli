package llm_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestContentPartJSONRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		part llm.ContentPart
	}{
		{
			name: "text",
			part: llm.ContentPart{
				Type: llm.ContentTypeText,
				Text: "hello",
			},
		},
		{
			name: "thinking with signature",
			part: llm.ContentPart{
				Type:      llm.ContentTypeThinking,
				Text:      "reasoning",
				Signature: "opaque-thinking-state",
			},
		},
		{
			name: "image",
			part: llm.ContentPart{
				Type: llm.ContentTypeImage,
				Image: &llm.ImageContent{
					Data:     []byte("image data"),
					MIMEType: "image/png",
				},
			},
		},
		{
			name: "tool call",
			part: llm.ContentPart{
				Type: llm.ContentTypeToolCall,
				ToolCall: &llm.ToolCall{
					ID:        "call-1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"README.md"}`),
					Signature: "opaque-tool-state",
				},
			},
		},
		{
			name: "tool result",
			part: llm.ContentPart{
				Type: llm.ContentTypeToolResult,
				ToolResult: &llm.ToolResult{
					CallID: "call-1",
					Name:   "read",
					Content: []llm.ContentPart{
						{
							Type: llm.ContentTypeText,
							Text: "file contents",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(tt.part)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var decoded llm.ContentPart
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if !reflect.DeepEqual(decoded, tt.part) {
				t.Errorf("JSON round trip = %#v, want %#v", decoded, tt.part)
			}
		})
	}
}

func TestRequestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	temperature := 0.2
	want := llm.Request{
		Model: llm.Model{
			ID:               "model-1",
			Name:             "Example Model",
			API:              llm.API("custom-chat-api"),
			Provider:         llm.ProviderID("custom-provider"),
			SupportsThinking: true,
			InputModalities: []llm.InputModality{
				llm.InputModalityText,
				llm.InputModalityImage,
			},
			ContextWindow: 128_000,
			MaxTokens:     8_192,
		},
		SystemPrompt: "You are a coding agent.",
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					{
						Type: llm.ContentTypeText,
						Text: "Inspect the repository.",
					},
				},
			},
		},
		Tools: []llm.ToolDefinition{
			{
				Name:        "read",
				Description: "Read a workspace file.",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
		Options: llm.StreamOptions{
			Temperature: &temperature,
			MaxTokens:   4_096,
			Thinking:    llm.ThinkingLevelHigh,
		},
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got llm.Request
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON round trip = %#v, want %#v", got, want)
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := llm.Event{
		Type:         llm.EventTypeDone,
		ContentIndex: 2,
		Usage: &llm.Usage{
			InputTokens:      120,
			OutputTokens:     30,
			ReasoningTokens:  10,
			CacheReadTokens:  50,
			CacheWriteTokens: 20,
			TotalTokens:      150,
			Cost: &llm.Cost{
				Input:      0.001,
				Output:     0.002,
				CacheRead:  0.0001,
				CacheWrite: 0.0002,
				Total:      0.0033,
			},
		},
		StopReason: llm.StopReasonStop,
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got llm.Event
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON round trip = %#v, want %#v", got, want)
	}
}
