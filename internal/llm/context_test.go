package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func usageMessage(provider llm.ProviderID, model string, timestamp, tokens int64) llm.AssistantMessage {
	return llm.AssistantMessage{
		Role: llm.RoleAssistant, API: "test-api", Provider: provider, ModelID: model,
		Content: []llm.ContentPart{llm.NewTextContent("data").Part()},
		Usage:   llm.Usage{TotalTokens: tokens}, StopReason: llm.StopReasonStop, Timestamp: timestamp,
	}
}

func TestEstimateContextTokensRequiresMatchingModelIdentity(t *testing.T) {
	t.Parallel()
	assistant := usageMessage("provider", "requested-model", 100, 500)
	assistant.ResponseModelID = "resolved-alias"
	for _, test := range []struct {
		name      string
		model     llm.Model
		wantUsage int64
	}{
		{"requested identity matches despite response alias", llm.Model{Provider: "provider", ID: "requested-model"}, 500},
		{"different provider", llm.Model{Provider: "other", ID: "requested-model"}, 0},
		{"different model", llm.Model{Provider: "provider", ID: "other"}, 0},
		{"response alias is not request identity", llm.Model{Provider: "provider", ID: "resolved-alias"}, 0},
		{"missing provider", llm.Model{ID: "requested-model"}, 0},
		{"missing model", llm.Model{Provider: "provider"}, 0},
		{"no identity", llm.Model{}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := llm.Request{
				Model: test.model, SystemPrompt: "12345678",
				Tools:    []llm.ToolDefinition{{Name: "tool", Description: "desc", InputSchema: json.RawMessage(`{}`)}},
				Messages: []llm.Message{assistant},
			}
			got := llm.EstimateContextTokens(request)
			if got.UsageTokens != test.wantUsage {
				t.Fatalf("estimate=%#v", got)
			}
			// Fallback counts system (2), tool name/description/schema (3), body (1).
			if test.wantUsage == 0 && (got.Tokens != 6 || got.TrailingTokens != 6 || got.LastUsageIndex != -1) {
				t.Fatalf("incomplete fallback=%#v", got)
			}
			if test.wantUsage > 0 && (got.Tokens != 500 || got.LastUsageIndex != 0) {
				t.Fatalf("matching usage=%#v", got)
			}
		})
	}
}

func TestEstimateContextTokensSelectsMatchingUsageInMixedHistory(t *testing.T) {
	t.Parallel()
	messages := []llm.Message{
		usageMessage("provider", "a", 100, 100),
		usageMessage("provider", "b", 101, 900),
		usageMessage("other-provider", "a", 102, 1000),
	}
	for _, test := range []struct {
		model                 llm.Model
		wantTokens, wantUsage int64
		wantIndex             int
	}{
		{llm.Model{Provider: "provider", ID: "a"}, 102, 100, 0},
		{llm.Model{Provider: "provider", ID: "b"}, 901, 900, 1},
		{llm.Model{Provider: "other-provider", ID: "a"}, 1000, 1000, 2},
	} {
		got := llm.EstimateContextTokens(llm.Request{Model: test.model, Messages: messages})
		if got.Tokens != test.wantTokens || got.UsageTokens != test.wantUsage || got.LastUsageIndex != test.wantIndex {
			t.Fatalf("model=%#v estimate=%#v", test.model, got)
		}
	}
}

func TestEstimateContextTokensUsesNewAssistantAfterCompactionWithoutUserInput(t *testing.T) {
	t.Parallel()
	summary := llm.CompactionSummaryMessage{Role: llm.RoleCompactionSummary, Summary: "earlier work", TokensBefore: 50000, Timestamp: 200}
	old := usageMessage("provider", "model", 100, 50000)
	for _, test := range []struct {
		name         string
		newAssistant llm.AssistantMessage
		wantUsage    int64
	}{
		{"fresh response in same interaction", usageMessage("provider", "model", 201, 50), 50},
		{"equal timestamp stays conservative", usageMessage("provider", "model", 200, 50), 0},
		{"other model response", usageMessage("provider", "other", 201, 50), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{summary, old, test.newAssistant})
			if err != nil {
				t.Fatal(err)
			}
			got := llm.EstimateContextTokens(llm.Request{Model: llm.Model{Provider: "provider", ID: "model"}, Messages: projected})
			if got.UsageTokens != test.wantUsage {
				t.Fatalf("estimate=%#v", got)
			}
			if test.wantUsage > 0 && (got.Tokens != 50 || got.LastUsageIndex != 2) {
				t.Fatalf("fresh estimate=%#v", got)
			}
		})
	}
}

func TestEstimateContextTokensIgnoresFailedAssistantUsage(t *testing.T) {
	t.Parallel()
	for _, reason := range []llm.StopReason{llm.StopReasonError, llm.StopReasonAborted} {
		assistant := usageMessage("provider", "model", 100, 9999)
		assistant.StopReason = reason
		got := llm.EstimateContextTokens(llm.Request{Model: llm.Model{Provider: "provider", ID: "model"}, Messages: []llm.Message{assistant}})
		if got.Tokens != 1 || got.UsageTokens != 0 || got.LastUsageIndex != -1 {
			t.Fatalf("reason=%s estimate=%#v", reason, got)
		}
	}
}

func TestContextBudgets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ window, reserve, safety int64 }{
		{-1, 0, 0}, {0, 0, 0}, {1, 1, 1}, {3, 1, 1}, {16, 4, 1},
		{10000, 2500, 625}, {65535, 16383, 4095}, {65536, 16384, 4096}, {100000, 16384, 4096},
	} {
		reserve, safety := llm.ContextBudgets(test.window)
		if reserve != test.reserve || safety != test.safety {
			t.Fatalf("window=%d reserve=%d safety=%d", test.window, reserve, safety)
		}
	}
}

func TestEstimateContextTokensUsesLastAssistantUsage(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		Model:        llm.Model{Provider: "test-provider", ID: "test-model"},
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

	got := llm.EstimateContextTokens(llm.Request{Model: llm.Model{Provider: "test-provider", ID: "test-model"}, Messages: projected})
	if got.UsageTokens != 0 || got.LastUsageIndex != -1 {
		t.Fatalf("EstimateContextTokens() reused pre-compaction usage: %#v", got)
	}
}

func TestEstimateContextTokensFallsBackToContent(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		Model:        llm.Model{Provider: "test-provider", ID: "test-model"},
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
		Model: llm.Model{Provider: "test-provider", ID: "test-model"},
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
