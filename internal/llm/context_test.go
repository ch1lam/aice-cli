package llm_test

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEstimateContextTokensUsesLastAssistantUsage(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		SystemPrompt: "the provider already counted this system prompt",
		Messages: []llm.Message{
			llm.UserMessage{
				Role:      llm.RoleUser,
				Content:   []llm.ContentPart{llm.NewTextContent("old prompt").Part()},
				Timestamp: 100,
			},
			llm.AssistantMessage{
				Role:       llm.RoleAssistant,
				API:        "test-api",
				Provider:   "test-provider",
				ModelID:    "test-model",
				Content:    []llm.ContentPart{llm.NewTextContent("old answer").Part()},
				Usage:      llm.Usage{TotalTokens: 100},
				StopReason: llm.StopReasonStop,
				Timestamp:  101,
			},
			llm.ToolResultMessage{
				Role:       llm.RoleToolResult,
				ToolCallID: "call-1",
				Content:    []llm.ContentPart{llm.NewTextContent("12345678").Part()},
				Timestamp:  102,
			},
			llm.UserMessage{
				Role:      llm.RoleUser,
				Content:   []llm.ContentPart{llm.NewTextContent("next").Part()},
				Timestamp: 103,
			},
		},
	}

	got := llm.EstimateContextTokens(request)
	if got.Tokens != 103 ||
		got.UsageTokens != 100 ||
		got.TrailingTokens != 3 ||
		got.LastUsageIndex != 1 {
		t.Fatalf("EstimateContextTokens() = %#v", got)
	}
}

func TestEstimateContextTokensResetsUsageAfterCompaction(t *testing.T) {
	t.Parallel()

	projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "older work is summarized",
			TokensBefore: 50_000,
			Timestamp:    200,
		},
	})
	if err != nil {
		t.Fatalf("AgentMessagesToMessages() error = %v", err)
	}
	projected = append(projected,
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			API:        "test-api",
			Provider:   "test-provider",
			ModelID:    "test-model",
			Content:    []llm.ContentPart{llm.NewTextContent("retained answer").Part()},
			Usage:      llm.Usage{TotalTokens: 50_000},
			StopReason: llm.StopReasonStop,
			Timestamp:  100,
		},
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("continue").Part()},
			Timestamp: 201,
		},
	)

	got := llm.EstimateContextTokens(llm.Request{Messages: projected})
	if got.UsageTokens != 0 || got.LastUsageIndex != -1 {
		t.Fatalf("EstimateContextTokens() reused pre-compaction usage: %#v", got)
	}
}

func TestEstimateContextTokensFallsBackToContent(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		SystemPrompt: "12345678",
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("1234").Part()},
			},
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("你好").Part()},
			},
		},
	}

	got := llm.EstimateContextTokens(request)
	if got.Tokens != 5 ||
		got.UsageTokens != 0 ||
		got.TrailingTokens != 5 ||
		got.LastUsageIndex != -1 {
		t.Fatalf("EstimateContextTokens() = %#v", got)
	}
}

func TestEstimateContextTokensIgnoresUsageBeforeNewerPrefix(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		Messages: []llm.Message{
			llm.UserMessage{
				Role:      llm.RoleUser,
				Content:   []llm.ContentPart{llm.NewTextContent("new summary").Part()},
				Timestamp: 200,
			},
			llm.AssistantMessage{
				Role:       llm.RoleAssistant,
				API:        "test-api",
				Provider:   "test-provider",
				ModelID:    "test-model",
				Content:    []llm.ContentPart{llm.NewTextContent("old answer").Part()},
				Usage:      llm.Usage{TotalTokens: 50_000},
				StopReason: llm.StopReasonStop,
				Timestamp:  100,
			},
		},
	}

	got := llm.EstimateContextTokens(request)
	if got.UsageTokens != 0 || got.LastUsageIndex != -1 {
		t.Fatalf("EstimateContextTokens() reused stale usage: %#v", got)
	}
}
