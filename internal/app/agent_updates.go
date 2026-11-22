package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// agentUpdateBuffer absorbs the bursts of children streaming at once; the
// frontend drains it continuously, so a full buffer only paces them.
const agentUpdateBuffer = 256

// followAgents makes the Session publish its subagents to a frontend that
// shows them. The returned stop must run once the frontend stops receiving,
// so no child waits for room after that.
func (s *interactiveSession) followAgents() (stop func()) {
	s.agentUpdates = make(chan interaction.AgentUpdate, agentUpdateBuffer)
	closed := make(chan struct{})
	s.agentsClosed = closed
	var once sync.Once
	return func() { once.Do(func() { close(closed) }) }
}

// AgentUpdates delivers every subagent run of the Session in order.
func (s *interactiveSession) AgentUpdates() <-chan interaction.AgentUpdate {
	if s == nil {
		return nil
	}
	return s.agentUpdates
}

// publishAgent waits for room for one update until ctx ends or the frontend
// stopped receiving. It never fails the child: a frontend that is gone must
// not stop it.
func (s *interactiveSession) publishAgent(ctx context.Context, update interaction.AgentUpdate) {
	select {
	case s.agentUpdates <- update:
	case <-ctx.Done():
	case <-s.agentsClosed:
	}
}

// agentPublisher is publishAgent for a Session whose frontend follows
// subagents, and nil otherwise.
func (s *interactiveSession) agentPublisher() func(context.Context, interaction.AgentUpdate) {
	if s.agentUpdates == nil {
		return nil
	}
	return s.publishAgent
}

// toolDescriber describes tool calls in the Session's workspace.
func (s *interactiveSession) toolDescriber() toolDescriber {
	return toolDescriber{guard: s.guard, workspace: s.workspacePath}
}

// syncAgentID names a synchronous child to the frontend. Call IDs repeat
// across turns with some providers, so the child gets its own.
func syncAgentID() string {
	var value [3]byte
	_, _ = rand.Read(value[:]) // crypto/rand.Read never fails since Go 1.24.
	return "agent-" + hex.EncodeToString(value[:])
}

// followChild publishes one child run, if a frontend follows subagents: its
// start at once, its events through the returned sink, and its end through
// settle, which runs once the run returned. The end waits for the frontend
// even when the run was cancelled, so no child is left shown as running.
func (d *delegation) followChild(ctx context.Context, request tool.DelegateRequest, child childRun, model configuredModel, catalog *mcpCatalog, prompt string) (agent.AgentEventSink, func(tool.DelegateResult)) {
	if d.publish == nil {
		return nil, func(tool.DelegateResult) {}
	}
	kind := tool.AgentGeneral
	if request.ReadOnly {
		kind = tool.AgentExplore
	}
	start := time.Now()
	d.publish(ctx, interaction.AgentUpdate{ID: child.id, Start: &interaction.AgentStart{
		ToolCallID:  request.CallID,
		Description: request.Description,
		Type:        kind,
		Fork:        request.Fork,
		Background:  child.background,
		Model:       interaction.DisplayModel{ID: model.model.ID, Provider: string(model.model.Provider)},
		Prompt:      prompt,
	}})
	sink := func(eventCtx context.Context, event agent.AgentEvent) error {
		if display := d.describe.display(event, catalog); display != nil {
			d.publish(eventCtx, interaction.AgentUpdate{ID: child.id, Event: display})
		}
		return nil
	}
	settle := func(res tool.DelegateResult) {
		d.publish(context.WithoutCancel(ctx), interaction.AgentUpdate{ID: child.id, End: &interaction.AgentEnd{
			Err: res.Err, Duration: time.Since(start), Turns: res.Turns, ToolCalls: res.ToolCalls,
		}})
	}
	return sink, settle
}
