package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// ruleExplore denies a write requested by an explore subagent.
const ruleExplore = "agent.explore"

// subagentPrompt follows the parent's system prompt in a child run.
const subagentPrompt = `

# Subagent

You are a subagent: the parent agent delegated the task in the first user message to you. No user can answer questions here; resolve what you can and state what you could not. Your final message is the report the parent receives instead of this whole conversation: give conclusions with the evidence behind them (paths, lines, commands, results), not a narrative of your steps.`

// childToolsExcluded never reach a subagent: it cannot delegate further, ask
// the user, or own background tasks that outlive its run.
var childToolsExcluded = map[string]bool{"agent": true, "request_user_input": true, "task_output": true, "task_kill": true}

// delegation is one main run's frozen settings for the subagents it starts.
// A child runs inside the parent's tool call, sharing its Guard, ask
// handler, model settings and MCP binding.
type delegation struct {
	newLoop   func(tools []agent.Tool, guard agent.Guard) (*agent.Loop, error)
	compactor func(store *session.Store) agent.HistoryCompactor
	// onUsage, when set, receives what each child spent, for frontends that
	// total usage from events rather than from the Session.
	onUsage      func(llm.Usage)
	tools        []agent.Tool
	workspace    *tool.Workspace
	guard        *guardAdapter
	model        llm.Model
	options      llm.StreamOptions
	systemPrompt string
	mcp          mcpRunBinding
	// sessionDir holds child Sessions; empty keeps them in memory, as for a
	// parent without a Session.
	sessionDir string
}

type delegationKey struct{}

func withDelegation(ctx context.Context, d *delegation) context.Context {
	return context.WithValue(ctx, delegationKey{}, d)
}

// delegationRouter is the agent tool's Delegator: it runs the child with the
// settings of the main run that made the call.
type delegationRouter struct{}

func (delegationRouter) Delegate(ctx context.Context, request tool.DelegateRequest) tool.DelegateResult {
	d, _ := ctx.Value(delegationKey{}).(*delegation)
	if d == nil {
		return tool.DelegateResult{Err: errors.New("subagents are not available in this run")}
	}
	return d.run(ctx, request)
}

func (d *delegation) run(ctx context.Context, request tool.DelegateRequest) (res tool.DelegateResult) {
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()
	var guard agent.Guard = d.guard
	if request.ReadOnly {
		guard = exploreGuard{inner: d.guard}
	}
	loop, err := d.newLoop(d.childTools(ctx), guard)
	if err != nil {
		res.Err = err
		return res
	}
	store, err := d.createStore(ctx)
	if err != nil {
		res.Err = err
		return res
	}
	if store != nil {
		defer func() { res.Err = errors.Join(res.Err, store.Close()) }()
		info, err := store.Info()
		if err != nil {
			res.Err = err
			return res
		}
		res.SessionID, res.Transcript = info.Header.ID, store.Path()
		if rel, err := filepath.Rel(d.workspace.Path(), store.Path()); err == nil {
			res.Transcript = rel
		}
	}
	var memory []llm.AgentMessage
	readMemory := func() []llm.AgentMessage { return memory }
	parentTranscript, _ := ctx.Value(transcriptContextKey{}).(runTranscript)
	ctx = withDelegation(ctx, nil)
	ctx, err = modelSessionContext(ctx, store)
	if err != nil {
		res.Err = err
		return res
	}
	ctx = withResultReader(ctx, store, readMemory)
	ctx = context.WithValue(ctx, transcriptContextKey{}, childTranscript(parentTranscript, store, readMemory))

	prompt, err := llm.NewUserMessage(llm.NewTextContent(request.Prompt).Part())
	if err != nil {
		res.Err = err
		return res
	}
	input := runScope{mcp: d.mcp, store: store}.input(d.model, d.systemPrompt+subagentPrompt, d.options, nil, prompt)
	input.MessageRecorder = func(recordCtx context.Context, message llm.AgentMessage) error {
		if store != nil {
			if err := appendSessionMessage(recordCtx, store, message); err != nil {
				return err
			}
		} else {
			memory = append(memory, message)
		}
		switch message := message.(type) {
		case llm.AssistantMessage:
			res.Turns++
			if text := visibleText(message.Content, "\n"); text != "" {
				res.Report = text
			}
		case llm.ToolResultMessage:
			res.ToolCalls++
		}
		return nil
	}
	input.Compactor = d.compactor(store)
	result, err := loop.Run(ctx, input, nil)
	res.Usage = result.Usage
	if d.onUsage != nil {
		d.onUsage(result.Usage)
	}
	res.Err = err
	return res
}

// childTools are the parent's host tools without those a child must not
// have; bash loses background execution.
func (d *delegation) childTools(ctx context.Context) []agent.Tool {
	tools := make([]agent.Tool, 0, len(d.tools))
	for _, current := range d.tools {
		name := current.Definition().Name
		if childToolsExcluded[name] {
			continue
		}
		if bash, ok := current.(*tool.Bash); ok && bash.SupportsBackground() {
			foreground, err := tool.NewBash(ctx, d.workspace, hostBashShells(), nil)
			if err != nil {
				current = tool.NewUnavailable(name, err)
			} else {
				current = foreground
			}
		}
		tools = append(tools, current)
	}
	return tools
}

// createStore starts the child Session under the agents directory, which
// the Session picker does not list.
func (d *delegation) createStore(ctx context.Context) (*session.Store, error) {
	if d.sessionDir == "" {
		return nil, nil
	}
	id, err := session.NewID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(d.sessionDir, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("app: create subagent Session directory: %w", err)
	}
	return session.Create(ctx, filepath.Join(dir, id+".jsonl"), session.Metadata{
		ID: id, CreatedAt: time.Now().UnixMilli(), WorkingDirectory: d.workspace.Path(),
	})
}

// childTranscript is what auto mode reviews a child call against: the
// parent's active branch, then the child's own actions. The child's task
// prompt is model-written, so it never counts as user intent.
func childTranscript(parent runTranscript, store *session.Store, memory func() []llm.AgentMessage) runTranscript {
	return func() ([]session.MessageEntry, error) {
		var entries []session.MessageEntry
		if parent != nil {
			parentEntries, err := parent()
			if err != nil {
				return nil, err
			}
			entries = append(entries, parentEntries...)
		}
		own, err := activeEntries(store, memory)
		if err != nil {
			return nil, err
		}
		for _, entry := range own {
			if _, user := entry.Message.(llm.UserMessage); !user {
				entries = append(entries, entry)
			}
		}
		return entries, nil
	}
}

// exploreGuard denies anything an explore subagent could change: file
// writes, bash commands not known to be read-only, and MCP tools, whose
// effects AICE cannot see. Everything else follows the parent's Guard.
type exploreGuard struct {
	inner *guardAdapter
}

func (g exploreGuard) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	res, hints, err := g.inner.evaluate(ctx, call)
	if err != nil || res.Decision == agent.GuardDeny {
		return res, err
	}
	reason := ""
	switch {
	case hints.mcp:
		reason = "an explore subagent cannot call MCP tools, whose effects are unknown"
	case call.Name == "bash" && !hints.readOnly:
		reason = "an explore subagent runs only read-only bash commands"
	}
	for _, action := range res.Actions {
		if action.Kind == "file" && action.Op == "write" {
			reason = "an explore subagent cannot write files"
		}
	}
	if reason != "" {
		return agent.GuardResult{Decision: agent.GuardDeny, RuleID: ruleExplore, Actions: res.Actions,
			Reason: reason + "; report what should change instead"}, nil
	}
	return g.inner.Check(ctx, call)
}
