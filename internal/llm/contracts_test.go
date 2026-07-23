package llm_test

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
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

func TestCoreMessageConstructors(t *testing.T) {
	t.Parallel()

	text := llm.NewTextContent("hello")
	if text.Type != llm.ContentTypeText || text.Text != "hello" {
		t.Fatalf("NewTextContent() = %#v", text)
	}

	user, err := llm.NewUserMessage(text.Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	if user.Role != llm.RoleUser || user.Timestamp == 0 {
		t.Errorf("NewUserMessage() = %#v", user)
	}
	if user.Content[0].Text != "hello" {
		t.Errorf("NewUserMessage() content = %#v", user.Content)
	}

	model := llm.Model{
		ID:       "model-1",
		API:      "example-api",
		Provider: "example-provider",
	}
	assistant := llm.NewAssistantMessage(model)
	if assistant.Role != llm.RoleAssistant || assistant.ModelID != model.ID || assistant.Timestamp == 0 {
		t.Errorf("NewAssistantMessage() = %#v", assistant)
	}
	if err := assistant.Validate(); err != nil {
		t.Errorf("AssistantMessage.Validate() error = %v", err)
	}

	resultMessage, err := llm.NewToolResultMessage(llm.ToolResult{
		CallID:  "call-1",
		Name:    "read",
		Content: []llm.ContentPart{llm.NewTextContent("file contents").Part()},
	})
	if err != nil {
		t.Fatalf("NewToolResultMessage() error = %v", err)
	}
	if resultMessage.Role != llm.RoleToolResult || resultMessage.ToolCallID != "call-1" {
		t.Errorf("NewToolResultMessage() = %#v", resultMessage)
	}
}

func TestConcreteMessageValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		validate func() error
		wantErr  bool
	}{
		{
			name: "valid user text",
			validate: func() error {
				return llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}.Validate()
			},
		},
		{
			name: "thinking in user message",
			validate: func() error {
				return llm.UserMessage{
					Role: llm.RoleUser,
					Content: []llm.ContentPart{
						llm.NewThinkingContent("plan", "signature").Part(),
					},
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "conflicting text payload",
			validate: func() error {
				return llm.UserMessage{
					Role: llm.RoleUser,
					Content: []llm.ContentPart{{
						Type:  llm.ContentTypeText,
						Text:  "hello",
						Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
					}},
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "missing tool call id",
			validate: func() error {
				return llm.AssistantMessage{
					Role:     llm.RoleAssistant,
					API:      "example-api",
					Provider: "example-provider",
					ModelID:  "model-1",
					Content: []llm.ContentPart{{
						Type: llm.ContentTypeToolCall,
						ToolCall: &llm.ToolCall{
							Name:      "read",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "wrong tool result role",
			validate: func() error {
				return llm.ToolResultMessage{
					Role:       llm.RoleUser,
					ToolCallID: "call-1",
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "valid empty tool result",
			validate: func() error {
				return llm.ToolResultMessage{
					Role:       llm.RoleToolResult,
					ToolCallID: "call-1",
				}.Validate()
			},
		},
		{
			name: "wrong user role",
			validate: func() error {
				return llm.UserMessage{
					Role:    llm.RoleAssistant,
					Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "empty user content",
			validate: func() error {
				return llm.UserMessage{
					Role: llm.RoleUser,
				}.Validate()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
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
			llm.UserMessage{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					{
						Type: llm.ContentTypeText,
						Text: "Inspect the repository.",
					},
				},
				Timestamp: 1_721_234_567_890,
			},
			llm.AssistantMessage{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Type: llm.ContentTypeToolCall,
					ToolCall: &llm.ToolCall{
						ID:        "call-1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"README.md"}`),
						Signature: "opaque-tool-state",
					},
				}},
				API:             "custom-chat-api",
				Provider:        "custom-provider",
				ModelID:         "model-1",
				ResponseModelID: "model-1-20260723",
				ResponseID:      "response-1",
				Usage: llm.Usage{
					InputTokens:  120,
					OutputTokens: 30,
					TotalTokens:  150,
					Cost: &llm.Cost{
						Input: 0.001,
						Total: 0.001,
					},
				},
				StopReason:   llm.StopReasonToolUse,
				ErrorMessage: "redacted provider diagnostic",
				Timestamp:    1_721_234_567_891,
			},
			llm.ToolResultMessage{
				Role:       llm.RoleToolResult,
				ToolCallID: "call-1",
				ToolName:   "read",
				Content: []llm.ContentPart{{
					Type: llm.ContentTypeText,
					Text: "file contents",
				}},
				IsError:   true,
				Timestamp: 1_721_234_567_892,
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

	roundTrip, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(round trip) error = %v", err)
	}
	if !bytes.Equal(roundTrip, encoded) {
		t.Errorf("JSON round trip = %s, want %s", roundTrip, encoded)
	}
}

func TestAgentMessagesJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("inspect").Part()},
			Timestamp: 1_721_234_567_890,
		},
		llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				llm.NewThinkingContent("reasoning", "thinking-signature").Part(),
				{
					Type: llm.ContentTypeToolCall,
					ToolCall: &llm.ToolCall{
						ID:        "call-1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"README.md"}`),
						Signature: "tool-signature",
					},
				},
			},
			API:             "custom-chat-api",
			Provider:        "custom-provider",
			ModelID:         "requested-model",
			ResponseModelID: "resolved-model",
			ResponseID:      "response-1",
			Usage: llm.Usage{
				InputTokens:  100,
				OutputTokens: 20,
				TotalTokens:  120,
				Cost: &llm.Cost{
					Input: 0.001,
					Total: 0.001,
				},
			},
			StopReason:   llm.StopReasonToolUse,
			ErrorMessage: "redacted provider diagnostic",
			Timestamp:    1_721_234_567_891,
		},
		llm.ToolResultMessage{
			Role:       llm.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    []llm.ContentPart{llm.NewTextContent("contents").Part()},
			Timestamp:  1_721_234_567_892,
		},
	}

	encoded, err := llm.MarshalAgentMessages(want)
	if err != nil {
		t.Fatalf("MarshalAgentMessages() error = %v", err)
	}
	got, err := llm.UnmarshalAgentMessages(encoded)
	if err != nil {
		t.Fatalf("UnmarshalAgentMessages() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent message round trip = %#v, want %#v", got, want)
	}
}

func TestAgentMessagesJSONRejectsInvalidMessage(t *testing.T) {
	t.Parallel()

	_, err := llm.MarshalAgentMessages([]llm.AgentMessage{
		llm.UserMessage{
			Role:    llm.RoleUser,
			Content: nil,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "message 0") {
		t.Fatalf("MarshalAgentMessages() error = %v, want validation error", err)
	}

	_, err = llm.UnmarshalAgentMessages([]byte(
		`[{"role":"unknown","content":[],"timestamp":1721234567890}]`,
	))
	if err == nil || !strings.Contains(err.Error(), "unsupported role") {
		t.Fatalf("UnmarshalAgentMessages() error = %v, want role error", err)
	}
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()

	validRequest := func() llm.Request {
		temperature := 0.2
		return llm.Request{
			Model: llm.Model{
				ID:            "model-1",
				API:           "custom-chat-api",
				Provider:      "custom-provider",
				ContextWindow: 128_000,
				MaxTokens:     8_192,
			},
			Messages: []llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			}},
			Tools: []llm.ToolDefinition{{
				Name:        "read",
				Description: "Read a workspace file.",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}},
			Options: llm.StreamOptions{
				Temperature: &temperature,
				MaxTokens:   4_096,
				Thinking:    llm.ThinkingLevelMedium,
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*llm.Request)
		want   string
	}{
		{name: "valid request"},
		{
			name: "missing model id",
			mutate: func(request *llm.Request) {
				request.Model.ID = ""
			},
			want: "model id is required",
		},
		{
			name: "missing model api",
			mutate: func(request *llm.Request) {
				request.Model.API = ""
			},
			want: "model api is required",
		},
		{
			name: "missing model provider",
			mutate: func(request *llm.Request) {
				request.Model.Provider = ""
			},
			want: "model provider is required",
		},
		{
			name: "negative context window",
			mutate: func(request *llm.Request) {
				request.Model.ContextWindow = -1
			},
			want: "model context window cannot be negative",
		},
		{
			name: "missing max tokens",
			mutate: func(request *llm.Request) {
				request.Model.MaxTokens = 0
				request.Options.MaxTokens = 0
			},
			want: "max tokens must be positive",
		},
		{
			name: "negative request max tokens",
			mutate: func(request *llm.Request) {
				request.Options.MaxTokens = -1
			},
			want: "max tokens cannot be negative",
		},
		{
			name: "non-finite temperature",
			mutate: func(request *llm.Request) {
				temperature := math.Inf(1)
				request.Options.Temperature = &temperature
			},
			want: "temperature must be finite",
		},
		{
			name: "unsupported thinking level",
			mutate: func(request *llm.Request) {
				request.Options.Thinking = "extreme"
			},
			want: "unsupported thinking level",
		},
		{
			name: "missing messages",
			mutate: func(request *llm.Request) {
				request.Messages = nil
			},
			want: "at least one message is required",
		},
		{
			name: "invalid message",
			mutate: func(request *llm.Request) {
				message := request.Messages[0].(llm.UserMessage)
				message.Content[0].Image = &llm.ImageContent{
					Data:     []byte("image"),
					MIMEType: "image/png",
				}
				request.Messages[0] = message
			},
			want: "message 0",
		},
		{
			name: "missing tool name",
			mutate: func(request *llm.Request) {
				request.Tools[0].Name = ""
			},
			want: "tool 0: name is required",
		},
		{
			name: "invalid tool schema",
			mutate: func(request *llm.Request) {
				request.Tools[0].InputSchema = json.RawMessage(`{"type":`)
			},
			want: "input schema must be a json object",
		},
		{
			name: "non-object tool schema",
			mutate: func(request *llm.Request) {
				request.Tools[0].InputSchema = json.RawMessage(`[]`)
			},
			want: "input schema must be a json object",
		},
		{
			name: "duplicate tool name",
			mutate: func(request *llm.Request) {
				request.Tools = append(request.Tools, request.Tools[0])
			},
			want: "duplicate tool name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := validRequest()
			if tt.mutate != nil {
				tt.mutate(&request)
			}

			err := request.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want text %q", err, tt.want)
			}
		})
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := llm.Event{
		Type:         llm.EventTypeDone,
		ContentIndex: 2,
		Content: &llm.ContentPart{
			Type:      llm.ContentTypeThinking,
			Text:      "reasoning",
			Signature: "opaque-signature",
		},
		StopReason: llm.StopReasonStop,
		Message: &llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Type: llm.ContentTypeText,
				Text: "answer",
			}},
			API:      "custom-chat-api",
			Provider: "custom-provider",
			ModelID:  "model-1",
			Usage: llm.Usage{
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
			Timestamp:  1_721_234_567_890,
		},
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got llm.Event
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	roundTrip, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(round trip) error = %v", err)
	}
	if !bytes.Equal(roundTrip, encoded) {
		t.Errorf("JSON round trip = %s, want %s", roundTrip, encoded)
	}
}

func TestEventJSONPreservesZeroContentIndex(t *testing.T) {
	t.Parallel()

	event := llm.Event{
		Type:         llm.EventTypeTextStart,
		ContentIndex: 0,
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"content_index":0`)) {
		t.Fatalf("json.Marshal() = %s, want explicit zero content index", encoded)
	}
}
