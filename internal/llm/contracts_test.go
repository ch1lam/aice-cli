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
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "Earlier work completed the provider adapter.",
			TokensBefore: 120_000,
			Timestamp:    1_721_234_567_893,
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

func TestAgentMessagesToMessagesProjectsCompactionSummary(t *testing.T) {
	t.Parallel()

	summary := llm.CompactionSummaryMessage{
		Role:         llm.RoleCompactionSummary,
		Summary:      "Earlier work completed the provider adapter.",
		TokensBefore: 120_000,
		Timestamp:    1_721_234_567_893,
	}
	user := llm.UserMessage{
		Role:      llm.RoleUser,
		Content:   []llm.ContentPart{llm.NewTextContent("continue").Part()},
		Timestamp: 1_721_234_567_894,
	}

	messages, err := llm.AgentMessagesToMessages([]llm.AgentMessage{
		summary,
		user,
	})
	if err != nil {
		t.Fatalf("AgentMessagesToMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("projected messages = %d, want 2", len(messages))
	}
	projected, ok := messages[0].(llm.UserMessage)
	if !ok {
		t.Fatalf("projected summary type = %T, want UserMessage", messages[0])
	}
	if projected.Timestamp != summary.Timestamp ||
		len(projected.Content) != 1 ||
		!strings.Contains(projected.Content[0].Text, summary.Summary) ||
		!strings.Contains(projected.Content[0].Text, "<summary>") {
		t.Fatalf("projected summary = %#v", projected)
	}
	if !reflect.DeepEqual(messages[1], user) {
		t.Fatalf("projected user = %#v, want %#v", messages[1], user)
	}
}

func TestAgentMessagesToMessagesOmitsFailedAttemptAndPairedToolResults(t *testing.T) {
	t.Parallel()

	failed := llm.AssistantMessage{
		Role:       llm.RoleAssistant,
		API:        "test-api",
		Provider:   "test-provider",
		ModelID:    "test-model",
		StopReason: llm.StopReasonError,
		Content: []llm.ContentPart{{
			Type: llm.ContentTypeToolCall,
			ToolCall: &llm.ToolCall{
				ID:        "call-failed",
				Name:      "write",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			},
		}},
	}
	paired := llm.ToolResultMessage{
		Role:       llm.RoleToolResult,
		ToolCallID: "call-failed",
		ToolName:   "write",
		Content:    []llm.ContentPart{llm.NewTextContent("not executed").Part()},
		IsError:    true,
	}
	user := llm.UserMessage{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.NewTextContent("start").Part()},
	}
	succeeded := llm.AssistantMessage{
		Role:       llm.RoleAssistant,
		API:        "test-api",
		Provider:   "test-provider",
		ModelID:    "test-model",
		StopReason: llm.StopReasonStop,
		Content:    []llm.ContentPart{llm.NewTextContent("done").Part()},
	}

	projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{user, failed, paired, succeeded})
	if err != nil {
		t.Fatalf("AgentMessagesToMessages() error = %v", err)
	}
	if len(projected) != 2 ||
		!reflect.DeepEqual(projected[0], user) ||
		!reflect.DeepEqual(projected[1], succeeded) {
		t.Fatalf("projected messages = %#v, want user and successful assistant", projected)
	}
}

func TestAgentMessagesToMessagesPreservesTerminalFailure(t *testing.T) {
	t.Parallel()

	failed := llm.AssistantMessage{
		Role:         llm.RoleAssistant,
		API:          "test-api",
		Provider:     "test-provider",
		ModelID:      "test-model",
		StopReason:   llm.StopReasonError,
		ErrorMessage: "request failed",
	}
	user := llm.UserMessage{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.NewTextContent("try something else").Part()},
	}

	projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{failed, user})
	if err != nil {
		t.Fatalf("AgentMessagesToMessages() error = %v", err)
	}
	if len(projected) != 2 || !reflect.DeepEqual(projected[0], failed) {
		t.Fatalf("projected messages = %#v, want terminal failure preserved", projected)
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

	_, err = llm.MarshalAgentMessages([]llm.AgentMessage{
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			TokensBefore: 100,
			Timestamp:    1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "summary is required") {
		t.Fatalf("MarshalAgentMessages() error = %v, want summary validation", err)
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

func TestSupportedThinkingLevels(t *testing.T) {
	t.Parallel()

	mappedLevels := llm.ThinkingLevelsMap(
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	mappedLevels[llm.ThinkingLevelMedium] = llm.ThinkingValue("high")
	mappedLevels[llm.ThinkingLevelXHigh] = llm.ThinkingValue("high")

	tests := []struct {
		name  string
		model llm.Model
		want  []llm.ThinkingLevel
	}{
		{
			name: "undeclared defaults to off through high",
			model: llm.Model{
				SupportsThinking: true,
			},
			want: []llm.ThinkingLevel{
				llm.ThinkingLevelOff,
				llm.ThinkingLevelMinimal,
				llm.ThinkingLevelLow,
				llm.ThinkingLevelMedium,
				llm.ThinkingLevelHigh,
			},
		},
		{
			name: "declared map limits the supported set",
			model: llm.Model{
				SupportsThinking: true,
				ThinkingLevelMap: llm.ThinkingLevelsMap(
					llm.ThinkingLevelHigh,
					llm.ThinkingLevelMax,
				),
			},
			want: []llm.ThinkingLevel{
				llm.ThinkingLevelHigh,
				llm.ThinkingLevelMax,
			},
		},
		{
			name: "mapped inputs collapse to distinct effective levels",
			model: llm.Model{
				SupportsThinking: true,
				ThinkingLevelMap: mappedLevels,
			},
			want: []llm.ThinkingLevel{
				llm.ThinkingLevelOff,
				llm.ThinkingLevelLow,
				llm.ThinkingLevelHigh,
				llm.ThinkingLevelMax,
			},
		},
		{
			name: "explicitly unsupported map returns no levels",
			model: llm.Model{
				SupportsThinking: true,
				ThinkingLevelMap: llm.ThinkingLevelsMap(),
			},
			want: []llm.ThinkingLevel{},
		},
		{
			name:  "non-thinking model supports only off",
			model: llm.Model{},
			want:  []llm.ThinkingLevel{llm.ThinkingLevelOff},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := llm.SupportedThinkingLevels(tt.model); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SupportedThinkingLevels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestThinkingLevelMapSemantics(t *testing.T) {
	t.Parallel()

	t.Run("nil map uses standard defaults", func(t *testing.T) {
		t.Parallel()

		var m llm.ThinkingLevelMap
		for _, level := range []llm.ThinkingLevel{
			llm.ThinkingLevelOff,
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
		} {
			if !m.Supports(level) {
				t.Errorf("Supports(%q) = false on nil map", level)
			}
			if value, ok := m.WireValue(level); !ok || value != string(level) {
				t.Errorf(
					"WireValue(%q) = %q/%v, want canonical %q",
					level,
					value,
					ok,
					level,
				)
			}
		}
		for _, level := range []llm.ThinkingLevel{
			llm.ThinkingLevelUnknown,
			llm.ThinkingLevelXHigh,
			llm.ThinkingLevelMax,
			llm.ThinkingLevel("extreme"),
		} {
			if m.Supports(level) {
				t.Errorf("Supports(%q) = true on nil map", level)
			}
			if _, ok := m.WireValue(level); ok {
				t.Errorf("WireValue(%q) reported supported on nil map", level)
			}
		}
	})

	t.Run("missing key and explicit null differ", func(t *testing.T) {
		t.Parallel()

		m := llm.ThinkingLevelMap{
			llm.ThinkingLevelHigh: llm.ThinkingValue("high"),
			llm.ThinkingLevelMax:  nil,
		}
		// Missing standard keys stay supported with canonical wire values.
		if !m.Supports(llm.ThinkingLevelMedium) {
			t.Error("Supports(medium) = false for a missing standard key")
		}
		if value, ok := m.WireValue(llm.ThinkingLevelMedium); !ok || value != "medium" {
			t.Errorf(
				"WireValue(medium) = %q/%v, want canonical medium",
				value,
				ok,
			)
		}
		// An explicit nil marks the level unsupported.
		if m.Supports(llm.ThinkingLevelMax) {
			t.Error("Supports(max) = true for an explicit null")
		}
		if _, ok := m.WireValue(llm.ThinkingLevelMax); ok {
			t.Error("WireValue(max) reported supported for an explicit null")
		}
	})

	t.Run("canonical wire mapping resolves redundant inputs", func(t *testing.T) {
		t.Parallel()

		m := llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		)
		m[llm.ThinkingLevelMedium] = llm.ThinkingValue("high")
		m[llm.ThinkingLevelXHigh] = llm.ThinkingValue("high")
		for _, level := range []llm.ThinkingLevel{
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelXHigh,
		} {
			if value, ok := m.WireValue(level); !ok || value != "high" {
				t.Errorf("WireValue(%q) = %q/%v, want high/true", level, value, ok)
			}
		}
	})

	t.Run("clone is independent", func(t *testing.T) {
		t.Parallel()

		m := llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh)
		cloned := m.Clone()
		m[llm.ThinkingLevelOff] = llm.ThinkingValue("none")
		if value, ok := cloned.WireValue(llm.ThinkingLevelOff); !ok || value != "off" {
			t.Errorf(
				"cloned WireValue(off) = %q/%v, want canonical off",
				value,
				ok,
			)
		}
		if value, _ := m.WireValue(llm.ThinkingLevelOff); value != "none" {
			t.Errorf("original WireValue(off) = %q, want none", value)
		}
	})

	t.Run("json preserves tri-state", func(t *testing.T) {
		t.Parallel()

		m := llm.ThinkingLevelMap{
			llm.ThinkingLevelOff: llm.ThinkingValue("none"),
			llm.ThinkingLevelMax: nil,
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var decoded map[string]*string
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if decoded["off"] == nil || *decoded["off"] != "none" {
			t.Errorf("decoded off = %#v, want none", decoded["off"])
		}
		if decoded["max"] != nil {
			t.Errorf("decoded max = %#v, want explicit null", decoded["max"])
		}
	})
}

func TestClampThinkingLevel(t *testing.T) {
	t.Parallel()

	defaultFive := llm.Model{SupportsThinking: true}
	alwaysThinking := llm.Model{
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		),
	}
	toggleOnly := llm.Model{
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelOff,
			llm.ThinkingLevelHigh,
		),
	}
	allSeven := llm.Model{
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelOff,
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
			llm.ThinkingLevelMax,
		),
	}
	noLevels := llm.Model{
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(),
	}
	mappedLevels := llm.ThinkingLevelsMap(
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	mappedLevels[llm.ThinkingLevelMedium] = llm.ThinkingValue("high")
	mappedLevels[llm.ThinkingLevelXHigh] = llm.ThinkingValue("high")
	mappedModel := llm.Model{
		SupportsThinking: true,
		ThinkingLevelMap: mappedLevels,
	}
	tests := []struct {
		name  string
		model llm.Model
		level llm.ThinkingLevel
		want  llm.ThinkingLevel
	}{
		{
			name:  "supported level passes through",
			model: defaultFive,
			level: llm.ThinkingLevelMedium,
			want:  llm.ThinkingLevelMedium,
		},
		{
			name:  "xhigh clamps down to high by default",
			model: defaultFive,
			level: llm.ThinkingLevelXHigh,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "max clamps down to high by default",
			model: defaultFive,
			level: llm.ThinkingLevelMax,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "always-thinking model rounds low up to high",
			model: alwaysThinking,
			level: llm.ThinkingLevelLow,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "always-thinking model rounds off up to high",
			model: alwaysThinking,
			level: llm.ThinkingLevelOff,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "always-thinking model keeps max",
			model: alwaysThinking,
			level: llm.ThinkingLevelMax,
			want:  llm.ThinkingLevelMax,
		},
		{
			name:  "always-thinking model clamps xhigh down to max",
			model: alwaysThinking,
			level: llm.ThinkingLevelXHigh,
			want:  llm.ThinkingLevelMax,
		},
		{
			name:  "toggle-only model rounds low up to high",
			model: toggleOnly,
			level: llm.ThinkingLevelLow,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "toggle-only model keeps off",
			model: toggleOnly,
			level: llm.ThinkingLevelOff,
			want:  llm.ThinkingLevelOff,
		},
		{
			name:  "toggle-only model clamps max down to high",
			model: toggleOnly,
			level: llm.ThinkingLevelMax,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "all-levels model passes every level through",
			model: allSeven,
			level: llm.ThinkingLevelMax,
			want:  llm.ThinkingLevelMax,
		},
		{
			name:  "mapped medium resolves to high",
			model: mappedModel,
			level: llm.ThinkingLevelMedium,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "mapped xhigh resolves to high",
			model: mappedModel,
			level: llm.ThinkingLevelXHigh,
			want:  llm.ThinkingLevelHigh,
		},
		{
			name:  "empty supported set falls back to off",
			model: noLevels,
			level: llm.ThinkingLevelHigh,
			want:  llm.ThinkingLevelOff,
		},
		{
			name:  "non-thinking model clamps every level to off",
			model: llm.Model{},
			level: llm.ThinkingLevelHigh,
			want:  llm.ThinkingLevelOff,
		},
		{
			name:  "unknown level stays unknown",
			model: defaultFive,
			level: llm.ThinkingLevelUnknown,
			want:  llm.ThinkingLevelUnknown,
		},
		{
			name:  "unrecognized level falls back to the lowest supported",
			model: defaultFive,
			level: llm.ThinkingLevel("extreme"),
			want:  llm.ThinkingLevelOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := llm.ClampThinkingLevel(tt.model, tt.level); got != tt.want {
				t.Errorf("ClampThinkingLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}
