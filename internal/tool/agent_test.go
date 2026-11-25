package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type delegatorFunc func(context.Context, tool.DelegateRequest) tool.DelegateResult

func (f delegatorFunc) Delegate(ctx context.Context, request tool.DelegateRequest) tool.DelegateResult {
	return f(ctx, request)
}

func (delegatorFunc) Start(_ context.Context, request tool.DelegateRequest) (tool.DelegateStart, error) {
	return tool.DelegateStart{Task: tool.TaskStatus{ID: "bg-000001", OutputPath: "/tasks/bg-000001.log"}, Notes: []string{"isolation: " + request.Isolation}}, nil
}

func agentCall(t *testing.T, arguments any) llm.ToolCall {
	t.Helper()
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return llm.ToolCall{ID: "call", Name: "agent", Arguments: data}
}

func TestAgentValidatesBeforeDelegating(t *testing.T) {
	t.Parallel()
	delegated := false
	agentTool, err := tool.NewAgent(delegatorFunc(func(context.Context, tool.DelegateRequest) tool.DelegateResult {
		delegated = true
		return tool.DelegateResult{}
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []map[string]any{
		{"description": "x", "prompt": " "},
		{"description": "", "prompt": "task"},
		{"description": "x", "prompt": "task", "type": "admin"},
		{"description": "x", "prompt": "task", "extra": true},
		{"description": "x", "prompt": "task", "isolation": "container"},
		{"description": "x", "prompt": "task", "subagent_type": "explore", "isolation": "worktree"},
	} {
		if _, err := agentTool.Execute(t.Context(), agentCall(t, arguments)); err == nil {
			t.Errorf("arguments %v were accepted", arguments)
		}
	}
	if delegated {
		t.Fatal("an invalid call reached the delegator")
	}
}

// A child that stopped early is an error result that keeps its partial
// report and what it spent; cancellation is also returned to the Loop.
func TestAgentStoppedChildKeepsReportAndUsage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	var request tool.DelegateRequest
	agentTool, err := tool.NewAgent(delegatorFunc(func(ctx context.Context, r tool.DelegateRequest) tool.DelegateResult {
		request = r
		cancel()
		return tool.DelegateResult{Report: "half done", SessionID: "child", Usage: llm.Usage{TotalTokens: 9}, Turns: 1, Err: ctx.Err()}
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := agentTool.Execute(ctx, agentCall(t, map[string]any{"description": "probe", "prompt": "task", "subagent_type": "explore", "run_in_background": false}))
	if !errors.Is(err, context.Canceled) || !request.ReadOnly {
		t.Fatalf("err = %v, read-only = %v", err, request.ReadOnly)
	}
	text := result.Content[0].Text
	if !result.IsError || !strings.Contains(text, "stopped before finishing") || !strings.Contains(text, "half done") ||
		result.Details == nil || len(result.Details.Delegations) != 1 || result.Details.Delegations[0].Usage.TotalTokens != 9 {
		t.Fatalf("result = %q / %#v", text, result.Details)
	}
}

func TestAgentTruncatesLongReports(t *testing.T) {
	t.Parallel()
	agentTool, err := tool.NewAgent(delegatorFunc(func(context.Context, tool.DelegateRequest) tool.DelegateResult {
		return tool.DelegateResult{Report: strings.Repeat("é", tool.MaxAgentReportBytes), Transcript: "child.jsonl"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := agentTool.Execute(t.Context(), agentCall(t, map[string]any{"description": "probe", "prompt": "task", "run_in_background": false}))
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if result.IsError || !utf8.ValidString(text) || len(text) > tool.MaxAgentReportBytes+512 ||
		!strings.Contains(text, "report truncated") || !strings.Contains(text, "transcript: child.jsonl") {
		t.Fatalf("result = %d bytes, tail %q", len(text), text[len(text)-200:])
	}
}

func TestAgentStartsExploreInBackground(t *testing.T) {
	t.Parallel()
	agentTool, err := tool.NewAgent(delegatorFunc(func(context.Context, tool.DelegateRequest) tool.DelegateResult {
		t.Error("a background call ran synchronously")
		return tool.DelegateResult{}
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := agentTool.Execute(t.Context(), agentCall(t, map[string]any{"description": "probe", "prompt": "task", "subagent_type": "explore", "run_in_background": true}))
	if err != nil || result.IsError || !strings.Contains(result.Content[0].Text, "bg-000001") || result.Details != nil {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

// The requested isolation reaches the delegator, and its notes reach the
// parent with the result.
func TestAgentPassesIsolationAndNotes(t *testing.T) {
	t.Parallel()
	agentTool, err := tool.NewAgent(delegatorFunc(func(_ context.Context, request tool.DelegateRequest) tool.DelegateResult {
		return tool.DelegateResult{Report: "done", Notes: []string{"isolation: " + request.Isolation}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, background := range []bool{false, true} {
		result, err := agentTool.Execute(t.Context(), agentCall(t, map[string]any{
			"description": "change", "prompt": "task", "isolation": "worktree", "run_in_background": background,
		}))
		if err != nil || !strings.HasSuffix(result.Content[0].Text, "\nisolation: worktree") {
			t.Fatalf("background %v result = %q, %v", background, result.Content[0].Text, err)
		}
	}
}

// noBackground cannot start background subagents.
type noBackground struct{ delegatorFunc }

func (noBackground) Start(context.Context, tool.DelegateRequest) (tool.DelegateStart, error) {
	return tool.DelegateStart{}, tool.ErrNoBackground
}

// A call runs in the background unless it asks not to. Where the background
// is unavailable, a call that did not ask for it runs synchronously; one that
// did fails.
func TestAgentRunsInTheBackgroundByDefault(t *testing.T) {
	t.Parallel()
	ran := false
	synchronous := delegatorFunc(func(context.Context, tool.DelegateRequest) tool.DelegateResult {
		ran = true
		return tool.DelegateResult{Report: "done"}
	})
	agentTool, err := tool.NewAgent(synchronous)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agentTool.Execute(t.Context(), agentCall(t, map[string]any{"description": "probe", "prompt": "task"}))
	if err != nil || ran || !strings.Contains(result.Content[0].Text, "bg-000001") {
		t.Fatalf("default call ran synchronously = %v: %#v, %v", ran, result, err)
	}

	agentTool, err = tool.NewAgent(noBackground{synchronous})
	if err != nil {
		t.Fatal(err)
	}
	result, err = agentTool.Execute(t.Context(), agentCall(t, map[string]any{"description": "probe", "prompt": "task"}))
	if err != nil || !ran || !strings.HasPrefix(result.Content[0].Text, "done") {
		t.Fatalf("fallback ran = %v: %#v, %v", ran, result, err)
	}
	if _, err := agentTool.Execute(t.Context(), agentCall(t, map[string]any{"description": "probe", "prompt": "task", "run_in_background": true})); !errors.Is(err, tool.ErrNoBackground) {
		t.Fatalf("explicit background without one: err = %v", err)
	}
}
