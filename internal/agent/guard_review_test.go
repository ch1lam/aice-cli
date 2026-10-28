package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// sequenceGuard returns its results in order, repeating the last one.
type sequenceGuard struct {
	results []agent.GuardResult
	calls   int
}

func (g *sequenceGuard) Check(context.Context, llm.ToolCall) (agent.GuardResult, error) {
	result := g.results[min(g.calls, len(g.results)-1)]
	g.calls++
	return result, nil
}

func TestGuardReviewIsRecordedOnToolResults(t *testing.T) {
	t.Parallel()
	review := &llm.ToolReview{Mode: "auto", Decision: "allow", Stage: 1, Classifier: "m@v"}
	block := &llm.ToolReview{Mode: "auto", Decision: "block", Stage: 2, Reason: "not requested", Classifier: "m@v"}
	info := testModel()
	model := repeatedToolModel(info, []string{`{"a":1}`, `{"a":2}`})
	tool := newFakeTool("read", nil)
	guard := &sequenceGuard{results: []agent.GuardResult{
		{Decision: agent.GuardAllow, Review: review},
		{Decision: agent.GuardDeny, Reason: "Auto mode blocked this action: not requested", RuleID: "auto.block", Review: block},
	}}
	loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(guard))
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "inspect")), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed, blocked := result.ModelRounds[0].ToolResults[0], result.ModelRounds[1].ToolResults[0]
	if allowed.IsError || allowed.Details == nil || *allowed.Details.Review != *review || allowed.Details.State != llm.ExecutionReturned {
		t.Fatalf("allowed result details = %+v", allowed.Details)
	}
	if !blocked.IsError || blocked.Details == nil || *blocked.Details.Review != *block || blocked.Details.State != llm.ExecutionNotDispatched {
		t.Fatalf("blocked result details = %+v", blocked.Details)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("tool calls = %d, want only the allowed call", len(tool.calls))
	}
	// The review survives serialization with the result.
	data, err := json.Marshal(result.ModelRounds[1].ToolResults[0])
	if err != nil || !strings.Contains(string(data), `"review":{"mode":"auto","decision":"block"`) {
		t.Fatalf("serialized = %s, %v", data, err)
	}
}

func TestGuardDenialLimitStopsRun(t *testing.T) {
	t.Parallel()
	deny := agent.GuardResult{Decision: agent.GuardDeny, Reason: "blocked"}
	allow := agent.GuardResult{Decision: agent.GuardAllow}
	for _, tc := range []struct {
		name     string
		limit    agent.GuardDenialLimit
		sequence []agent.GuardResult
		stopAt   int // model requests before stopping; 0 means the run settles
	}{
		{"consecutive", agent.GuardDenialLimit{Consecutive: 3}, []agent.GuardResult{deny, deny, deny, deny}, 3},
		{"allowed call resets the streak", agent.GuardDenialLimit{Consecutive: 3}, []agent.GuardResult{deny, deny, allow, deny, deny}, 0},
		{"total", agent.GuardDenialLimit{Total: 3}, []agent.GuardResult{deny, allow, deny, allow, deny}, 5},
		{"disabled", agent.GuardDenialLimit{}, []agent.GuardResult{deny, deny, deny, deny, deny}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := testModel()
			args := make([]string, len(tc.sequence))
			for i := range args {
				args[i] = fmt.Sprintf(`{"n":%d}`, i)
			}
			model := repeatedToolModel(info, args)
			loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("read", nil)},
				agent.WithGuard(&sequenceGuard{results: tc.sequence}), agent.WithRunLimits(agent.RunLimits{GuardDenials: tc.limit}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = loop.Run(t.Context(), testInput(info, mustPrompt(t, "inspect")), nil)
			if tc.stopAt == 0 {
				if err != nil {
					t.Fatalf("run stopped: %v", err)
				}
				return
			}
			if !errors.Is(err, agent.ErrGuardDenials) || len(model.requests) != tc.stopAt {
				t.Fatalf("err=%v requests=%d, want ErrGuardDenials after %d", err, len(model.requests), tc.stopAt)
			}
		})
	}
	if _, err := agent.NewLoop(nil, nil, agent.WithRunLimits(agent.RunLimits{GuardDenials: agent.GuardDenialLimit{Total: -1}})); err == nil {
		t.Fatal("negative guard denial limit accepted")
	}
}
