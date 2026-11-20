package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// sharedWriterPrompt follows the subagent section for a background writer
// in the parent's workspace.
const sharedWriterPrompt = `

Other agents change files in this workspace while you run. Change only the files your task requires; a write to a file another agent is changing is denied, so do not work around that denial.`

// worktreePrompt follows the subagent section for a child in its own
// worktree: its workspace, the commit and the main workspace.
const worktreePrompt = `

You work in your own git worktree at %s, checked out from commit %s. The main workspace %s is not yours: a path you are given there has the same relative path here, and you read and change files here only. It has no uncommitted changes from the main workspace, and ignored files such as installed dependencies are absent until you create them. When you finish, AICE commits what you leave for the parent agent to review, so do not commit, push or switch branches yourself.`

// artifactsPrompt follows for a background writer; %s is its artifact
// directory.
const artifactsPrompt = `

Write test reports, logs and other artifacts under %s, which is yours alone, and name the ones that matter in your report.`

// exploreToolsExcluded leave an explore child, which may not write.
var exploreToolsExcluded = map[string]bool{"write": true, "edit": true}

// childToolsExcluded never reach a subagent: it cannot delegate further, ask
// the user, or own background tasks that outlive its run.
var childToolsExcluded = map[string]bool{"agent": true, "request_user_input": true, "task_output": true, "task_kill": true}

// mcpToolsExcluded also leave a child without MCP: they route through the
// parent run's MCP binding.
var mcpToolsExcluded = map[string]bool{"tool_search": true, "mcp_resource_list": true, "mcp_server_info": true}

// delegation is one main run's frozen settings for the subagents it starts.
// A synchronous child runs inside the parent's tool call, sharing its Guard,
// ask handler, model settings and MCP binding. A background child is a task
// that may outlive the run: it has no MCP binding and no person to ask.
type delegation struct {
	// newLoop builds a child Loop; asks routes Guard asks to the frontend,
	// otherwise they are refused.
	newLoop   func(tools []agent.Tool, guard agent.Guard, asks bool) (*agent.Loop, error)
	compactor func(store *session.Store) agent.HistoryCompactor
	// tasks runs background children; nil leaves only synchronous ones.
	tasks        *tool.Tasks
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
	// worktrees holds worktree children's checkouts; empty leaves every
	// child in the parent's workspace.
	worktrees string
}

type delegationKey struct{}

func withDelegation(ctx context.Context, d *delegation) context.Context {
	return context.WithValue(ctx, delegationKey{}, d)
}

// delegationRouter is the agent tool's Delegator: it runs the child with the
// settings of the main run that made the call.
type delegationRouter struct{}

// errNoDelegation also refuses a child that tries to delegate: subagents are
// one level deep.
var errNoDelegation = errors.New("subagents are not available in this run; a subagent cannot start another")

func (delegationRouter) Delegate(ctx context.Context, request tool.DelegateRequest) tool.DelegateResult {
	d, _ := ctx.Value(delegationKey{}).(*delegation)
	if d == nil {
		return tool.DelegateResult{Err: errNoDelegation}
	}
	tree, notes, err := d.isolate(ctx, request, false)
	if err != nil {
		return tool.DelegateResult{Err: err}
	}
	res := d.run(ctx, request, childRun{tree: tree})
	if tree != nil {
		notes = append(notes, tree.finish(ctx, request.Description))
	}
	res.Notes = append(notes, res.Notes...)
	return res
}

func (delegationRouter) Start(ctx context.Context, request tool.DelegateRequest) (tool.DelegateStart, error) {
	d, _ := ctx.Value(delegationKey{}).(*delegation)
	if d == nil {
		return tool.DelegateStart{}, errNoDelegation
	}
	return d.start(ctx, request)
}

// childRun is how one child runs.
type childRun struct {
	background bool
	// artifacts is a background writer's own directory.
	artifacts string
	// tree is the child's own worktree, if it has one.
	tree *worktree
}

// isolate decides where a writing child works: in its own worktree when
// asked, and by default in the background when the workspace is in a git
// repository; otherwise in the parent's workspace. notes tell the parent
// what the child will not see, or why a default worktree was not made.
func (d *delegation) isolate(ctx context.Context, request tool.DelegateRequest, background bool) (*worktree, []string, error) {
	explicit := request.Isolation == tool.IsolationWorktree
	if request.ReadOnly || !explicit && (request.Isolation == tool.IsolationNone || !background) {
		return nil, nil, nil
	}
	if d.worktrees == "" {
		if explicit {
			return nil, nil, errors.New("worktree subagents are unavailable in this run")
		}
		return nil, nil, nil
	}
	tree, err := newWorktree(ctx, d.workspace.Path(), d.worktrees)
	if err != nil {
		if explicit {
			return nil, nil, err
		}
		return nil, []string{"The subagent shares your workspace instead of a worktree: " + err.Error()}, nil
	}
	notes := []string{fmt.Sprintf("The subagent works in its own git worktree from commit %s; its changes come back as a branch.", tree.base[:12])}
	if dirtyWorkspace(ctx, d.workspace.Path()) {
		notes = append(notes, "Your workspace has uncommitted changes, which the subagent does not see.")
	}
	return tree, notes, nil
}

// start runs a child as a background task. It keeps the call's context
// values but not its cancellation: only the task's own stop ends it. Its
// report goes to the task's output file and its delegation record to
// whichever result first reports the completion. A writer gets its own
// artifact directory; one sharing the workspace is named by its task ID for
// file claims.
func (d *delegation) start(callCtx context.Context, request tool.DelegateRequest) (tool.DelegateStart, error) {
	if d.tasks == nil {
		return tool.DelegateStart{}, errors.New("background subagents are unavailable in this run; run it synchronously")
	}
	tree, notes, err := d.isolate(callCtx, request, true)
	if err != nil {
		return tool.DelegateStart{}, err
	}
	values := context.WithoutCancel(callCtx)
	status, err := d.tasks.StartRun(tool.TaskAgent, request.Description, func(taskCtx context.Context, id string, out io.Writer) (*llm.ToolDelegation, error) {
		ctx, cancel := context.WithCancel(values)
		defer cancel()
		defer context.AfterFunc(taskCtx, cancel)()
		child := childRun{background: true, tree: tree}
		if tree != nil {
			// Settle the worktree however the run ends; its note follows the report.
			defer func() { fmt.Fprintln(out, tree.finish(ctx, request.Description)) }()
		}
		if !request.ReadOnly {
			if tree == nil {
				d.guard.claims.begin(id)
				defer d.guard.claims.end(id)
				ctx = withWriter(ctx, id)
			}
			child.artifacts = filepath.Join(d.workspace.Path(), ".aice", "agents", id)
			if err := os.MkdirAll(child.artifacts, 0o700); err != nil {
				return nil, fmt.Errorf("create artifact directory: %w", err)
			}
		}
		res := d.run(ctx, request, child)
		report := tool.TruncateAgentReport(res.Report)
		if report == "" {
			report = "(the subagent finished without a report)"
		}
		fmt.Fprintf(out, "%s\n[subagent: %d turns, %d tool calls, %s", report, res.Turns, res.ToolCalls, res.Duration.Round(time.Second))
		if res.Transcript != "" {
			fmt.Fprintf(out, "; transcript: %s", res.Transcript)
		}
		if artifacts := child.artifacts; artifacts != "" {
			if rel, err := filepath.Rel(d.workspace.Path(), artifacts); err == nil {
				artifacts = rel
			}
			fmt.Fprintf(out, "; artifacts: %s", artifacts)
		}
		fmt.Fprintln(out, "]")
		return &llm.ToolDelegation{SessionID: res.SessionID, Usage: res.Usage, Turns: res.Turns, ToolCalls: res.ToolCalls}, res.Err
	})
	if err != nil {
		if tree != nil {
			tree.finish(callCtx, request.Description)
		}
		return tool.DelegateStart{}, err
	}
	return tool.DelegateStart{Task: status, Notes: notes}, nil
}

// run executes one child to completion.
func (d *delegation) run(ctx context.Context, request tool.DelegateRequest, child childRun) (res tool.DelegateResult) {
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()
	var guard agent.Guard = d.guard
	if request.ReadOnly {
		guard = exploreGuard{inner: d.guard}
	}
	// A background child may outlive the run whose MCP binding it would use,
	// and a worktree child's Guard has no MCP services bound.
	noMCP := child.background || child.tree != nil
	mcp := d.mcp
	if noMCP {
		mcp = mcpRunBinding{}
		ctx = context.WithValue(ctx, mcpRunContextKey{}, noMCPRun{})
	}
	tools := d.childTools(ctx, noMCP, request.ReadOnly)
	if child.tree != nil {
		var err error
		if tools, guard, err = d.inWorktree(ctx, child, tools); err != nil {
			res.Err = err
			return res
		}
	}
	loop, err := d.newLoop(tools, guard, !child.background)
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
	systemPrompt := d.systemPrompt + subagentPrompt
	switch {
	case child.tree != nil:
		systemPrompt += fmt.Sprintf(worktreePrompt, child.tree.workspace, child.tree.base[:12], d.workspace.Path())
	case child.background && !request.ReadOnly:
		systemPrompt += sharedWriterPrompt
	}
	if child.artifacts != "" {
		systemPrompt += fmt.Sprintf(artifactsPrompt, child.artifacts)
	}
	input := runScope{mcp: mcp, store: store}.input(d.model, systemPrompt, d.options, nil, prompt)
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
	res.Err = err
	return res
}

// noMCPRun unbinds the parent's MCP run from a background child's context.
type noMCPRun struct{}

// inWorktree rebinds a worktree child's file tools and Guard to its
// worktree. It may read the main workspace and write its artifact directory.
func (d *delegation) inWorktree(ctx context.Context, child childRun, tools []agent.Tool) ([]agent.Tool, agent.Guard, error) {
	workspace, err := tool.NewWorkspace(child.tree.workspace)
	if err != nil {
		return nil, nil, err
	}
	adapter, err := d.guard.rebase(workspace.PhysicalPath(), d.workspace.PhysicalPath())
	if err != nil {
		return nil, nil, err
	}
	if child.artifacts != "" {
		adapter.inner.AllowPathSession(child.artifacts, true, true)
	}
	return worktreeTools(ctx, tools, workspace), newWorktreeGuard(adapter, child.tree, d.workspace.PhysicalPath(), child.artifacts), nil
}

// childTools are the parent's host tools without those a child must not
// have; bash loses background execution.
func (d *delegation) childTools(ctx context.Context, noMCP, readOnly bool) []agent.Tool {
	tools := make([]agent.Tool, 0, len(d.tools))
	for _, current := range d.tools {
		name := current.Definition().Name
		if childToolsExcluded[name] || noMCP && mcpToolsExcluded[name] || readOnly && exploreToolsExcluded[name] {
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
