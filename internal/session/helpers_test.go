package session_test

import (
	"encoding/json"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"testing"
)

func textMessages() []llm.AgentMessage {
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("hello").Part()},
			Timestamp: 1_721_234_567_810,
		},
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			Content:    []llm.ContentPart{llm.NewTextContent("hello back").Part()},
			API:        "custom-chat-api",
			Provider:   "custom-provider",
			ModelID:    "requested-model",
			ResponseID: "response-text",
			Usage: llm.Usage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
			StopReason: llm.StopReasonStop,
			Timestamp:  1_721_234_567_820,
		},
	}
}

func toolMessages() []llm.AgentMessage {
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
		Signature: "tool-signature",
	}
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("inspect").Part()},
			Timestamp: 1_721_234_567_910,
		},
		llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				llm.NewThinkingContent("reasoning", "thinking-signature").Part(),
				{Type: llm.ContentTypeToolCall, ToolCall: &call},
			},
			API:             "custom-chat-api",
			Provider:        "custom-provider",
			ModelID:         "requested-model",
			ResponseModelID: "resolved-model",
			ResponseID:      "response-tool",
			Usage: llm.Usage{
				InputTokens:      20,
				OutputTokens:     10,
				ReasoningTokens:  4,
				CacheReadTokens:  3,
				CacheWriteTokens: 2,
				TotalTokens:      30,
				Cost: &llm.Cost{
					Input:      0.001,
					Output:     0.002,
					CacheRead:  0.0001,
					CacheWrite: 0.0002,
					Total:      0.0033,
				},
			},
			StopReason:   llm.StopReasonToolUse,
			ErrorMessage: "redacted provider diagnostic",
			Timestamp:    1_721_234_567_920,
		},
		llm.ToolResultMessage{
			Role:       llm.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    []llm.ContentPart{llm.NewTextContent("contents").Part()},
			Timestamp:  1_721_234_567_930,
		},
		llm.AssistantMessage{
			Role:            llm.RoleAssistant,
			Content:         []llm.ContentPart{llm.NewTextContent("done").Part()},
			API:             "custom-chat-api",
			Provider:        "custom-provider",
			ModelID:         "requested-model",
			ResponseModelID: "resolved-model",
			ResponseID:      "response-final",
			Usage: llm.Usage{
				InputTokens:  40,
				OutputTokens: 8,
				TotalTokens:  48,
				Cost: &llm.Cost{
					Input:  0.004,
					Output: 0.001,
					Total:  0.005,
				},
			},
			StopReason: llm.StopReasonStop,
			Timestamp:  1_721_234_567_940,
		},
	}
}
func mustCompaction(
	t *testing.T,
	input session.CompactionInput,
) session.Compaction {
	t.Helper()

	compaction, err := session.NewCompaction(input)
	if err != nil {
		t.Fatalf("NewCompaction() error = %v", err)
	}
	return compaction
}

func namedTextMessages(
	prompt string,
	answer string,
	usageTokens int64,
) []llm.AgentMessage {
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent(prompt).Part()},
			Timestamp: usageTokens*10 + 1,
		},
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			Content:    []llm.ContentPart{llm.NewTextContent(answer).Part()},
			API:        "custom-chat-api",
			Provider:   "custom-provider",
			ModelID:    "requested-model",
			Usage:      llm.Usage{TotalTokens: usageTokens},
			StopReason: llm.StopReasonStop,
			Timestamp:  usageTokens*10 + 2,
		},
	}
}

func assertSessionText(
	t *testing.T,
	message llm.AgentMessage,
	role llm.Role,
	text string,
) {
	t.Helper()

	switch value := message.(type) {
	case llm.UserMessage:
		if role != llm.RoleUser || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want user text %q", message, text)
		}
	case llm.AssistantMessage:
		if role != llm.RoleAssistant || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want assistant text %q", message, text)
		}
	default:
		t.Errorf("message = %#v, want role %q text %q", message, role, text)
	}
}
