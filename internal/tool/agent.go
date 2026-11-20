package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// MaxAgentReportBytes bounds the subagent report returned to the parent; the
// child Session keeps the complete transcript.
const MaxAgentReportBytes = 32 << 10

// Subagent types.
const (
	AgentGeneral = "general"
	AgentExplore = "explore"
)

// DelegateRequest is one subagent task.
type DelegateRequest struct {
	Description string
	Prompt      string
	// ReadOnly runs the child with every write denied.
	ReadOnly bool
}

// DelegateResult is what a subagent run produced. Err reports a run that
// stopped early; the other fields still describe what it did and spent.
type DelegateResult struct {
	Report    string
	SessionID string
	// Transcript is the child Session's path for display; empty in memory.
	Transcript string
	Usage      llm.Usage
	Turns      int
	ToolCalls  int
	Duration   time.Duration
	Err        error
}

// Delegator runs subagents. Delegate runs one to completion within the
// caller's context; Start runs one as a background task.
type Delegator interface {
	Delegate(ctx context.Context, request DelegateRequest) DelegateResult
	Start(ctx context.Context, request DelegateRequest) (TaskStatus, error)
}

// Agent delegates a self-contained task to a subagent with a fresh context.
type Agent struct {
	delegator Delegator
}

// NewAgent constructs the agent tool.
func NewAgent(delegator Delegator) (*Agent, error) {
	if delegator == nil {
		return nil, fmt.Errorf("tool %q: delegator is required", "agent")
	}
	return &Agent{delegator: delegator}, nil
}

// Definition returns the model-facing delegation contract.
func (a *Agent) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "agent",
		Description: "Delegate a self-contained task to a subagent that starts with a fresh context, works with your tools " +
			"until done, and returns only its final report. Its intermediate steps never enter your context. " +
			"type \"explore\" has no write tools, denies every write and is for research, locating code and review; \"general\" (default) may edit and run builds and tests. " +
			"run_in_background returns a task id at once; the report arrives with a later tool result. " +
			"Concurrent writers never change the same file: a write to a file another running writer changed is denied.",
		InputSchema: jsonSchema(`{
			"type": "object",
			"properties": {
				"description": {"type": "string", "description": "3-5 word label shown to the user"},
				"prompt": {"type": "string", "description": "The complete task. The subagent sees nothing else from this conversation: include the goal, relevant paths, constraints and what to report."},
				"type": {"type": "string", "enum": ["general", "explore"]},
				"run_in_background": {"type": "boolean", "description": "Run the subagent as a background task and return its task id at once (default: false)"}
			},
			"required": ["description", "prompt"],
			"additionalProperties": false
		}`),
		PromptSnippet: "Delegate a self-contained task to a subagent that returns only its final report",
		PromptGuidelines: []string{
			"Delegate only independent tasks whose process is long but whose conclusion is short, such as a broad search, an investigation or a review; do simple or tightly coupled work yourself",
			"Write the prompt so it stands alone: the subagent cannot see this conversation or ask the user anything",
			"Prefer type explore unless the task must change files; verify a subagent's claims before relying on them for edits",
			"Run independent subagents in the background to work in parallel, keep doing your own work, and use task_output to wait only when you need a report; never start the same task twice",
			"Give parallel writers disjoint files: name the files each may change, and do not edit them yourself until it finishes",
		},
	}
}

type agentArguments struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Type        string `json:"type,omitempty"`
	Background  bool   `json:"run_in_background,omitempty"`
}

// Execute runs one subagent. A child that stopped early is an error result
// that still carries its partial report and what it spent.
func (a *Agent) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeArguments[agentArguments](ctx, call, "agent")
	if err != nil {
		return llm.ToolResult{}, err
	}
	args.Description, args.Prompt = strings.TrimSpace(args.Description), strings.TrimSpace(args.Prompt)
	switch {
	case args.Prompt == "":
		return llm.ToolResult{}, fmt.Errorf("tool %q: prompt is required", "agent")
	case args.Description == "" || utf8.RuneCountInString(args.Description) > 80:
		return llm.ToolResult{}, fmt.Errorf("tool %q: description must be a short non-empty label", "agent")
	case args.Type != "" && args.Type != AgentGeneral && args.Type != AgentExplore:
		return llm.ToolResult{}, fmt.Errorf("tool %q: type must be %q or %q", "agent", AgentGeneral, AgentExplore)
	}
	request := DelegateRequest{Description: args.Description, Prompt: args.Prompt, ReadOnly: args.Type == AgentExplore}
	if args.Background {
		status, err := a.delegator.Start(ctx, request)
		if err != nil {
			return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "agent", err)
		}
		return textResult(call, fmt.Sprintf("Subagent started in background as task %s; its report will be written to %s.\n"+
			"Its report arrives with a later tool result. Continue your own work; use task_output with wait only when you need the report. Do not start it again.",
			status.ID, status.OutputPath), false), nil
	}
	res := a.delegator.Delegate(ctx, request)

	var text strings.Builder
	report := strings.TrimSpace(res.Report)
	if len(report) > MaxAgentReportBytes {
		cut := MaxAgentReportBytes
		for cut > 0 && !utf8.RuneStart(report[cut]) {
			cut--
		}
		report = report[:cut] + fmt.Sprintf("\n[report truncated: %d of %d bytes shown; the child transcript has the rest]", cut, len(res.Report))
	}
	if res.Err != nil {
		fmt.Fprintf(&text, "Subagent stopped before finishing: %v\n", res.Err)
		if report != "" {
			text.WriteString("Its last report:\n")
		}
	} else if report == "" {
		report = "(the subagent finished without a report)"
	}
	text.WriteString(report)
	fmt.Fprintf(&text, "\n[subagent %s: %d turns, %d tool calls, %s", map[bool]string{true: "stopped", false: "finished"}[res.Err != nil],
		res.Turns, res.ToolCalls, res.Duration.Round(time.Second))
	if res.Transcript != "" {
		fmt.Fprintf(&text, "; transcript: %s", res.Transcript)
	}
	text.WriteString("]")

	result := textResult(call, text.String(), res.Err != nil)
	result.Details = &llm.ToolResultDetails{State: llm.ExecutionReturned, Delegations: []llm.ToolDelegation{{
		SessionID: res.SessionID, Usage: res.Usage, Turns: res.Turns, ToolCalls: res.ToolCalls,
	}}}
	// Cancellation still records what the child did and spent.
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}
