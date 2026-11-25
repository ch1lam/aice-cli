package tool

import (
	"context"
	"errors"
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

// Subagent isolation modes.
const (
	IsolationNone     = "none"
	IsolationWorktree = "worktree"
)

// ErrNoBackground reports that a run cannot start background subagents; a
// call that did not ask for the background runs synchronously instead.
var ErrNoBackground = errors.New("background subagents are unavailable in this run")

// DelegateRequest is one subagent task.
type DelegateRequest struct {
	Description string
	Prompt      string
	// ReadOnly runs the child with every write denied.
	ReadOnly bool
	// Isolation is IsolationNone, IsolationWorktree, or empty for the
	// delegator's default.
	Isolation string
	// Fork starts the child with the caller's conversation instead of a
	// fresh context.
	Fork bool
	// CallID is the agent call that started the child.
	CallID string
}

// DelegateStart is a subagent started in the background.
type DelegateStart struct {
	Task TaskStatus
	// Notes tell the parent what it must know before the report arrives.
	Notes []string
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
	// Notes tell the parent what it must know besides the report, such as
	// where a worktree child's changes are.
	Notes []string
	Err   error
}

// Delegator runs subagents. Delegate runs one to completion within the
// caller's context; Start runs one as a background task.
type Delegator interface {
	Delegate(ctx context.Context, request DelegateRequest) DelegateResult
	Start(ctx context.Context, request DelegateRequest) (DelegateStart, error)
}

// Agent delegates a task to a subagent with a fresh context or, forked, with
// the caller's.
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
		Description: "Delegate a self-contained task (research across many files, a scoped implementation, a review) to a subagent " +
			"that works with your tools in its own fresh context, so the work does not consume yours: it returns only its final report, never its intermediate steps. " +
			"fork starts it instead with a copy of this conversation up to this call, on your model, sharing your prompt cache. " +
			"By default it runs in the background: the call returns a task id at once, and the report arrives later as a notification, also after you end your turn. " +
			"isolation \"worktree\" runs a general subagent in its own git worktree from the current commit: it sees no uncommitted changes, " +
			"and its changes come back as a branch for you to review and apply. It is the default for a background general subagent in a git repository. " +
			"Writers sharing the workspace never change the same file: a write to a file another running writer changed is denied.",
		InputSchema: jsonSchema(`{
			"type": "object",
			"properties": {
				"description": {"type": "string", "description": "3-5 word label shown to the user"},
				"prompt": {"type": "string", "description": "The complete task. Unless forked, the subagent sees nothing else from this conversation: include the goal, relevant paths, constraints and what to report. A fork knows the conversation, so say only what to do and report."},
				"fork": {"type": "boolean", "description": "Start the subagent with a copy of this conversation instead of a fresh context (default: false)"},
				"subagent_type": {"type": "string", "enum": ["general", "explore"], "description": "\"explore\" has no write tools and denies every write, for research, locating code and review; \"general\" (default) may edit and run builds and tests"},
				"run_in_background": {"type": "boolean", "description": "Run the subagent as a background task and return its task id at once (default: true); false waits for its report in this call and, unless it works in a worktree, gives it your MCP tools"},
				"isolation": {"type": "string", "enum": ["none", "worktree"], "description": "Where a general subagent changes files: \"worktree\" (default in the background) or \"none\" to share your workspace (default when synchronous)"}
			},
			"required": ["description", "prompt"],
			"additionalProperties": false
		}`),
		PromptSnippet: "Delegate a self-contained task to a subagent that works beside you and returns only its report",
		PromptGuidelines: []string{
			"Your context is the run's scarcest resource: every file you read and every long output stays in it for the rest of the task and crowds out what matters. A subagent spends its own context and returns only its conclusion. So delegate by default when the work splits into independent parts, when answering means reading across many files or searching without knowing where to look (subagent_type explore), when a command's output is long and you need only the failures, or when you want a review not anchored on your own conclusion: give the reviewer the code or diff and the question, not your verdict",
			"Do it yourself when it takes a few tool calls, when your very next step depends on it, or when the parts are tightly coupled",
			"Plan first: decide what you do yourself now and which independent parts can run beside you. Start independent subagents together in one response, then continue with work they do not cover",
			"A delegated part belongs to its subagent: do not read its files, rerun its searches or start it twice while it works. If its report falls short, send_message to it rather than redoing the work",
			"Write each prompt so it stands alone; brevity does not apply here, since the subagent knows only what you write. Give the goal and why it matters, what you already know or ruled out, the files or commands to start from, what is in and out of scope, whether to change files, and the report you need. Ask for evidence you can check (names, paths with lines, commands and their results) and for what it could not verify",
			"When only waiting remains, say what is still running and end your turn; each report arrives in full as a notification that resumes you. Do not wait for subagents with task_output, poll, sleep or guess a pending result, and collect the reports you need before your final answer",
			"A report can be confidently wrong. Check the claims your answer depends on; review a writer's changes or branch and run the relevant tests yourself before calling the work done. The user does not see reports: relay what matters",
			"Set run_in_background to false only when the subagent needs MCP tools or you have nothing else to do until it answers. Writers that share your workspace need disjoint files",
			"Fork when the task depends on what this conversation has established and restating it would be long or lossy, such as applying a plan you worked out to one part of the code. A fork carries your whole context into every step it takes, so start a fresh subagent when a short prompt can say what it needs, as for independent research",
		},
	}
}

type agentArguments struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Type        string `json:"subagent_type,omitempty"`
	Background  *bool  `json:"run_in_background,omitempty"`
	Isolation   string `json:"isolation,omitempty"`
	Fork        bool   `json:"fork,omitempty"`
}

// TruncateAgentReport trims a subagent report and cuts it at
// MaxAgentReportBytes with a pointer to the child transcript.
func TruncateAgentReport(report string) string {
	report = strings.TrimSpace(report)
	if len(report) <= MaxAgentReportBytes {
		return report
	}
	cut := MaxAgentReportBytes
	for cut > 0 && !utf8.RuneStart(report[cut]) {
		cut--
	}
	return report[:cut] + fmt.Sprintf("\n[report truncated: %d of %d bytes shown; the child transcript has the rest]", cut, len(report))
}

// Execute starts one subagent in the background, or runs it within the call
// when the call asks for that.
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
		return llm.ToolResult{}, fmt.Errorf("tool %q: subagent_type must be %q or %q", "agent", AgentGeneral, AgentExplore)
	case args.Isolation != "" && args.Isolation != IsolationNone && args.Isolation != IsolationWorktree:
		return llm.ToolResult{}, fmt.Errorf("tool %q: isolation must be %q or %q", "agent", IsolationNone, IsolationWorktree)
	case args.Isolation == IsolationWorktree && args.Type == AgentExplore:
		return llm.ToolResult{}, fmt.Errorf("tool %q: an explore subagent changes nothing, so it needs no worktree", "agent")
	}
	request := DelegateRequest{Description: args.Description, Prompt: args.Prompt, ReadOnly: args.Type == AgentExplore, Isolation: args.Isolation, Fork: args.Fork, CallID: call.ID}
	// The background is the default; without one, a call that did not ask
	// for it runs synchronously.
	if background := args.Background == nil || *args.Background; background {
		started, err := a.delegator.Start(ctx, request)
		if errors.Is(err, ErrNoBackground) && args.Background == nil {
			return a.delegate(ctx, call, request)
		}
		if err != nil {
			return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "agent", err)
		}
		text := fmt.Sprintf("Subagent started in background as task %s; its report will be written to %s.\n"+
			"Its report arrives as a notification, even after you end your turn. Its task is now its own: do not read the files or run the searches you gave it. Continue only with work it does not cover, and end your turn if only waiting remains; do not poll it or start it again.",
			started.Task.ID, started.Task.OutputPath)
		for _, note := range started.Notes {
			text += "\n" + note
		}
		return textResult(call, text, false), nil
	}
	return a.delegate(ctx, call, request)
}

// delegate runs one subagent within the call. A child that stopped early is
// an error result that still carries its partial report and what it spent.
func (a *Agent) delegate(ctx context.Context, call llm.ToolCall, request DelegateRequest) (llm.ToolResult, error) {
	res := a.delegator.Delegate(ctx, request)

	var text strings.Builder
	report := TruncateAgentReport(res.Report)
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
	for _, note := range res.Notes {
		text.WriteString("\n" + note)
	}

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
