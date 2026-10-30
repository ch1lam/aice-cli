package session_test

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestComputeStatsCountsEveryBranch(t *testing.T) {
	t.Parallel()
	cost := func(total float64) llm.Usage {
		return llm.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12, Cost: &llm.Cost{Total: total}}
	}
	entry := func(createdAt int64, message llm.AgentMessage) session.MessageEntry {
		return session.MessageEntry{CreatedAt: createdAt, Message: message}
	}
	snapshot := session.Snapshot{
		Header:     session.Header{ID: "s1"},
		RunConfigs: []session.RunConfig{{Build: "v1"}, {Build: "v1"}, {Build: "v2"}},
		Messages: []session.MessageEntry{
			entry(1_000, llm.UserMessage{Role: llm.RoleUser}),
			entry(2_000, llm.AssistantMessage{Provider: "p", ModelID: "m", StopReason: llm.StopReasonError, Usage: cost(0.5)}),
			entry(3_000, llm.AssistantMessage{Provider: "p", ModelID: "m", StopReason: llm.StopReasonToolUse, Usage: cost(1)}),
			entry(4_000, llm.ToolResultMessage{ToolName: "bash", Actions: []llm.ToolAction{
				{Kind: "command", Command: "cat a > b"}, {Kind: "file", Op: "write", Path: "b"}, {Kind: "file", Op: "read", Path: "a"},
			}}),
			entry(5_000, llm.ToolResultMessage{ToolName: "read", IsError: true,
				Details: &llm.ToolResultDetails{State: llm.ExecutionNotDispatched},
				Actions: []llm.ToolAction{{Kind: "file", Op: "read", Path: ".env"}}}),
			// An abandoned branch still billed its request.
			entry(6_000, llm.AssistantMessage{Provider: "q", ModelID: "n", StopReason: llm.StopReasonStop, Usage: cost(2)}),
		},
		Compactions: []session.Compaction{{CreatedAt: 9_000, Usage: cost(0.25)}},
	}
	got := session.ComputeStats(snapshot)
	want := session.Stats{
		SessionID: "s1", Builds: []string{"v1", "v2"}, Models: []string{"p/m", "q/n"},
		Usage:        session.TotalUsage(snapshot),
		UserMessages: 1, ModelRequests: 3, FailedRequests: 1, LastStopReason: llm.StopReasonStop,
		ToolCalls: 2, ToolErrors: 1, ToolsNotDispatched: 1,
		Tools:       map[string]int{"bash": 1, "read": 1},
		Actions:     map[string]int{"command": 1, "file:write": 1, "file:read": 2},
		Compactions: 1, DurationMS: 8_000,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stats =\n%+v\nwant\n%+v", got, want)
	}
	if got.Usage.Cost == nil || got.Usage.Cost.Total != 3.75 {
		t.Fatalf("usage cost = %+v, want every request and compaction", got.Usage.Cost)
	}
}
