package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// ruleExplore denies a write requested by an explore subagent.
const ruleExplore = "agent.explore"

// subagentPrompt follows the parent's system prompt in a child run.
const subagentPrompt = `

# Subagent

You are a subagent: the parent agent delegated the task in the first user message to you. Do it directly and completely within its scope; if you notice something outside it, note it in one sentence instead of doing it. The delegation guidance above is for the parent: you cannot start or message subagents, ask the user or wait on background tasks, so do the work yourself. Stop once you can answer the task with evidence; do not keep exploring for completeness. No user can answer questions here: make reasonable assumptions, state them, and state what you could not resolve. Your final message is the only thing the parent receives: start with the answer or outcome, then the evidence behind it (names, file paths with lines, commands and their results), then what you could not verify. If you changed files, list them and the checks you ran. Keep the report as short as its evidence allows; do not narrate your steps or recap code you merely read.`

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

// childToolsExcluded never reach a subagent: it cannot delegate further,
// message other subagents, ask the user, or own background tasks that
// outlive its run.
var childToolsExcluded = map[string]bool{"agent": true, "send_message": true, "request_user_input": true, "task_output": true, "task_kill": true}

// mcpToolsExcluded also leave a child without MCP: they route through the
// parent run's MCP binding.
var mcpToolsExcluded = map[string]bool{"tool_search": true, "mcp_resource_list": true, "mcp_server_info": true}

// delegation is one main run's frozen settings for the subagents it starts.
// A synchronous child runs inside the parent's tool call, sharing its Guard,
// ask handler, model settings and MCP binding. A background child is a task
// that may outlive the run: it has no MCP binding, and its asks reach the
// frontend labeled with its task.
type delegation struct {
	// models resolves the children's model once, on the first call that
	// needs it, so a bad subagent_model fails only the agent call. A fork
	// runs on parent, the main run's own model.
	models func() (configuredModel, error)
	parent configuredModel
	// newLoop builds a child Loop on that model with the frontend's ask
	// handler, if any.
	newLoop   func(model configuredModel, tools []agent.Tool, guard agent.Guard) (*agent.Loop, error)
	compactor func(store *session.Store, model *configuredModel) agent.HistoryCompactor
	// tasks runs background children; nil leaves only synchronous ones.
	tasks        *tool.Tasks
	tools        []agent.Tool
	workspace    *tool.Workspace
	guard        *guardAdapter
	systemPrompt string
	// webSearch is the run's provider-hosted search policy; children inherit
	// it like web_search, which read-only children also keep.
	webSearch *llm.WebSearch
	mcp       mcpRunBinding
	// store is the parent's Session, if any; sessionDir holds child
	// Sessions, and empty keeps them in memory, as for a parent without one.
	store      *session.Store
	sessionDir string
	// worktrees holds worktree children's checkouts; empty leaves every
	// child in the parent's workspace.
	worktrees string
	// publish delivers children's runs to a frontend that follows them; nil
	// when none does. describe projects their tool calls for it.
	publish  func(context.Context, interaction.AgentUpdate)
	describe toolDescriber
}

type delegationKey struct{}

// askerKey names the subagent whose Guard asks a context carries.
type askerKey struct{}

// asker is a subagent asking through the frontend: its label, and its own
// gate when it has one, which the grants it is given belong to.
type asker struct {
	label string
	gate  *guard.Guard
}

// askingAgent is the subagent label for a Guard ask, empty for the main agent.
func askingAgent(ctx context.Context) string {
	current, _ := ctx.Value(askerKey{}).(asker)
	return current.label
}

// askingGate is the gate a Guard ask came from: a worktree child's own, or
// fallback, the main agent's.
func askingGate(ctx context.Context, fallback *guard.Guard) *guard.Guard {
	if current, _ := ctx.Value(askerKey{}).(asker); current.gate != nil {
		return current.gate
	}
	return fallback
}

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
	inherit, err := d.prepare(ctx, request)
	if err != nil {
		return tool.DelegateResult{Err: err}
	}
	tree, notes, err := d.isolate(ctx, request, false)
	if err != nil {
		return tool.DelegateResult{Err: err}
	}
	res := d.run(ctx, request, childRun{id: syncAgentID(), tree: tree, label: request.Description, inherit: inherit, inherited: len(inherit.messages)}, request.Prompt)
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
	// id names the child to a frontend: its task, or the call that runs it.
	id         string
	background bool
	// agent is a background child's state across its turns.
	agent *subagent
	// label names the child in its Guard asks.
	label string
	// artifacts is a background writer's own directory.
	artifacts string
	// tree is the child's own worktree, if it has one.
	tree *worktree
	// inherit is a fork's conversation, which its first turn starts with;
	// the first inherited entries of its Session are that conversation.
	inherit   inheritance
	inherited int
}

// prepare checks what a child needs before it starts: its model, or a
// fork's inheritance, which it returns.
func (d *delegation) prepare(ctx context.Context, request tool.DelegateRequest) (inheritance, error) {
	if request.Fork {
		return inherit(ctx, d.store)
	}
	_, err := d.models()
	return inheritance{}, err
}

// childModel is the model a child runs on.
func (d *delegation) childModel(request tool.DelegateRequest) (configuredModel, error) {
	if request.Fork {
		return d.parent, nil
	}
	return d.models()
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
// values but not its cancellation: only the task's own stop ends it. The
// child stays with its task, so send_message can steer or continue it.
func (d *delegation) start(callCtx context.Context, request tool.DelegateRequest) (tool.DelegateStart, error) {
	if d.tasks == nil {
		return tool.DelegateStart{}, fmt.Errorf("%w; run it synchronously", tool.ErrNoBackground)
	}
	inherit, err := d.prepare(callCtx, request)
	if err != nil {
		return tool.DelegateStart{}, err
	}
	tree, notes, err := d.isolate(callCtx, request, true)
	if err != nil {
		return tool.DelegateStart{}, err
	}
	child := &subagent{request: request, tree: tree, delegation: d, running: true, inherit: inherit, inherited: len(inherit.messages)}
	status, err := d.tasks.StartRun(tool.TaskAgent, request.Description, child, d.background(context.WithoutCancel(callCtx), child, request.Prompt))
	if err != nil {
		if tree != nil {
			tree.finish(callCtx, request.Description)
		}
		return tool.DelegateStart{}, err
	}
	return tool.DelegateStart{Task: status, Notes: notes}, nil
}

// run executes one child turn to completion, with prompt as its input. A
// background child continues its own Session.
func (d *delegation) run(ctx context.Context, request tool.DelegateRequest, child childRun, text string) (res tool.DelegateResult) {
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()
	var gate agent.Guard = d.guard
	if request.ReadOnly {
		gate = exploreGuard{inner: d.guard}
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
	current := asker{label: child.label}
	if child.tree != nil {
		var err error
		var own *guardAdapter
		if tools, gate, own, err = d.inWorktree(ctx, child, tools); err != nil {
			res.Err = err
			return res
		}
		current.gate = own.inner
	}
	if request.Fork {
		tools = forkTools(d.tools, tools)
	}
	ctx = context.WithValue(ctx, askerKey{}, current)
	model, err := d.childModel(request)
	if err != nil {
		res.Err = err
		return res
	}
	loop, err := d.newLoop(model, tools, gate)
	if err != nil {
		res.Err = err
		return res
	}
	events, settle := d.followChild(ctx, request, child, model, mcp.catalog, text)
	// Runs after the Session closed, so the end reports a failed close too.
	defer func() { settle(res) }()
	store, history, err := d.childStore(ctx, child.agent, child.inherit)
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
	if store == nil {
		memory = slices.Clone(history)
	}
	readMemory := func() []llm.AgentMessage { return memory }
	parentTranscript, _ := ctx.Value(transcriptContextKey{}).(runTranscript)
	ctx = withDelegation(ctx, nil)
	// A fork continues the parent's conversation, so it keeps the parent's
	// routing identity, which a provider may route its prompt cache by.
	if !request.Fork {
		ctx, err = modelSessionContext(ctx, store)
		if err != nil {
			res.Err = err
			return res
		}
	}
	ctx = withResultReader(ctx, store, readMemory)
	ctx = context.WithValue(ctx, transcriptContextKey{}, childTranscript(parentTranscript, store, readMemory, child.inherited))

	// A fresh child reads its sections in its system prompt; a fork, whose
	// system prompt is the parent's, before the task in its first message.
	sections := subagentPrompt
	if request.Fork {
		sections = forkPrompt
		switch {
		case request.ReadOnly:
			sections += exploreForkPrompt
		case noMCP && d.mcp.summary != "":
			sections += forkNoMCPPrompt
		}
	}
	switch {
	case child.tree != nil:
		sections += fmt.Sprintf(worktreePrompt, child.tree.workspace, child.tree.base[:12], d.workspace.Path())
	case child.background && !request.ReadOnly:
		sections += sharedWriterPrompt
	}
	if child.artifacts != "" {
		sections += fmt.Sprintf(artifactsPrompt, child.artifacts)
	}
	systemPrompt := d.systemPrompt + sections
	if request.Fork {
		// The parent's system prompt includes its MCP summary, which a fork
		// without MCP keeps so the prompts stay equal.
		systemPrompt = d.systemPrompt
		if noMCP {
			systemPrompt += d.mcp.summary
		}
		if len(child.inherit.messages) > 0 {
			text = sections + forkTask + text
		}
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent(text).Part())
	if err != nil {
		res.Err = err
		return res
	}
	input := runScope{mcp: mcp, store: store}.input(model.model, systemPrompt, model.options, history, prompt)
	if d.webSearch != nil {
		search := d.webSearch.Clone()
		input.WebSearch = &search
	}
	if child.agent != nil {
		// Messages from the parent wait for the child's next step, or keep it
		// going when it would stop.
		input.Steering, input.FollowUp = child.agent.takeMessage, child.agent.takeMessage
	}
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
	input.Compactor = d.compactor(store, &model)
	result, err := loop.Run(ctx, input, events)
	res.Usage = result.Usage
	res.Err = err
	return res
}

// noMCPRun unbinds the parent's MCP run from a background child's context.
type noMCPRun struct{}

// inWorktree rebinds a worktree child's file tools and Guard to its
// worktree. It may read the main workspace and write its artifact directory.
// adapter is the gate its grants belong to.
func (d *delegation) inWorktree(ctx context.Context, child childRun, tools []agent.Tool) ([]agent.Tool, agent.Guard, *guardAdapter, error) {
	workspace, err := tool.NewWorkspace(child.tree.workspace)
	if err != nil {
		return nil, nil, nil, err
	}
	adapter, err := d.guard.rebase(workspace.PhysicalPath(), d.workspace.PhysicalPath())
	if err != nil {
		return nil, nil, nil, err
	}
	if child.artifacts != "" {
		adapter.inner.AllowPathSession(child.artifacts, true, true)
	}
	return worktreeTools(ctx, tools, workspace), newWorktreeGuard(adapter, child.tree, d.workspace.PhysicalPath(), child.artifacts), adapter, nil
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

// childStore opens a child's Session and its history. A synchronous child
// starts one under the agents directory, which the Session picker does not
// list, or keeps it in memory without a parent Session. A background child
// always has one, in the task directory without a parent Session, so later
// turns continue it; they repair a turn an interrupt cut short. A new Session
// starts with a fork's inheritance.
func (d *delegation) childStore(ctx context.Context, child *subagent, inherit inheritance) (*session.Store, []llm.AgentMessage, error) {
	if child == nil {
		store, err := d.createStore(ctx, d.sessionDir, inherit)
		return store, inherit.messages, err
	}
	if path := child.sessionPath(); path != "" {
		store, err := session.Open(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		history, err := func() ([]llm.AgentMessage, error) {
			if err := store.RecoverInterrupted(ctx); err != nil {
				return nil, err
			}
			snapshot, err := store.Snapshot()
			if err != nil {
				return nil, err
			}
			return sessionHistory(snapshot)
		}()
		if err != nil {
			return nil, nil, errors.Join(err, store.Close())
		}
		return store, history, nil
	}
	dir := d.sessionDir
	if dir == "" {
		dir = d.tasks.Dir()
	}
	store, err := d.createStore(ctx, dir, inherit)
	if err != nil {
		return nil, nil, err
	}
	child.setSessionPath(store.Path())
	return store, inherit.messages, nil
}

// createStore starts a child Session under dir's agents directory, with a
// fork's whole inheritance or not at all; an empty dir keeps it in memory.
func (d *delegation) createStore(ctx context.Context, sessionDir string, inherit inheritance) (*session.Store, error) {
	if sessionDir == "" {
		return nil, nil
	}
	id, err := session.NewID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(sessionDir, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("app: create subagent Session directory: %w", err)
	}
	path := filepath.Join(dir, id+".jsonl")
	store, err := session.Create(ctx, path, session.Metadata{
		ID: id, CreatedAt: time.Now().UnixMilli(), WorkingDirectory: d.workspace.Path(),
	})
	if err != nil {
		return nil, err
	}
	if err := inherit.record(ctx, store); err != nil {
		return nil, errors.Join(fmt.Errorf("app: start forked Session: %w", err), store.Close(), os.Remove(path))
	}
	return store, nil
}

// childTranscript is what auto mode reviews a child call against: the
// parent's active branch, then the child's own actions after the inherited
// entries of a fork, which the parent's branch already has. The child's
// task prompt is model-written, so it never counts as user intent.
func childTranscript(parent runTranscript, store *session.Store, memory func() []llm.AgentMessage, inherited int) runTranscript {
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
		for _, entry := range own[min(inherited, len(own)):] {
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
	return g.inner.settle(ctx, call, res, hints)
}
