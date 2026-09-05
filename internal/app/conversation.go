package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// conversationState owns the durable transcript, its derived history, and the
// main interaction's pending messages. historySyncMu serializes store/history
// updates; historyMu protects history and activeMainRun for side snapshots.
// When both locks are needed, acquire historySyncMu before historyMu, keeping
// file I/O outside historyMu. The zero value represents a session not yet started.
// A conversationState must not be copied after first use.
type conversationState struct {
	store         *session.Store
	historySyncMu sync.Mutex
	historyMu     sync.RWMutex
	history       []llm.AgentMessage
	activeMainRun *mainRunState
}

type mainRunState struct {
	pendingMessages []llm.AgentMessage
}

// beginMainRun registers the main interaction and freezes committed history.
func (c *conversationState) beginMainRun(
	prompt llm.UserMessage,
) (*mainRunState, []llm.AgentMessage, error) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	if c.activeMainRun != nil {
		return nil, nil, fmt.Errorf("app: another main run is active")
	}
	history, err := cloneAgentMessages(c.history)
	if err != nil {
		return nil, nil, err
	}
	pendingMessages, err := cloneAgentMessages([]llm.AgentMessage{prompt})
	if err != nil {
		return nil, nil, err
	}
	state := &mainRunState{pendingMessages: pendingMessages}
	c.activeMainRun = state
	return state, history, nil
}

// registerMainMessages makes accepted user input and complete model/tool turns
// visible to side snapshots while the current main interaction is still
// running. Streaming assistant output is never registered.
func (c *conversationState) registerMainMessages(
	state *mainRunState,
	messages []llm.AgentMessage,
) error {
	cloned, err := cloneAgentMessages(messages)
	if err != nil {
		return err
	}
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	if c.activeMainRun != state {
		return fmt.Errorf("app: main run state is no longer active")
	}
	state.pendingMessages = append(state.pendingMessages, cloned...)
	return nil
}

// endMainRun removes transient user inputs even when the run or Session
// persistence fails. The identity check prevents a stale run from clearing a
// newer owner.
func (c *conversationState) endMainRun(state *mainRunState) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	if c.activeMainRun == state {
		state.pendingMessages = []llm.AgentMessage{}
		c.activeMainRun = nil
	}
}

// commitHistory serializes durable Session updates without holding the
// in-memory history lock across file I/O. After persistence succeeds, one
// short critical section publishes the complete interaction and clears its
// transient inputs, so a side snapshot observes one consistent version.
func (c *conversationState) commitHistory(
	ctx context.Context,
	state *mainRunState,
	messages []llm.AgentMessage,
) error {
	cloned, err := cloneAgentMessages(messages)
	if err != nil {
		return err
	}
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	if err := appendSessionTurn(ctx, c.store, messages); err != nil {
		return err
	}

	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	c.history = append(c.history, cloned...)
	if c.activeMainRun == state {
		state.pendingMessages = []llm.AgentMessage{}
	}
	return nil
}

// sideSnapshot returns a deep clone of the committed parent history plus
// accepted user inputs and complete model/tool turns from the current main
// interaction. In-progress assistant output remains private to the main run.
func (c *conversationState) sideSnapshot() ([]llm.AgentMessage, error) {
	c.historyMu.RLock()
	defer c.historyMu.RUnlock()
	snapshot, err := cloneAgentMessages(c.history)
	if err != nil {
		return nil, err
	}
	if c.activeMainRun == nil {
		return snapshot, nil
	}
	pending, err := cloneAgentMessages(c.activeMainRun.pendingMessages)
	if err != nil {
		return nil, err
	}
	return append(snapshot, pending...), nil
}

// reloadHistory serializes with main interaction commits, rebuilds from the
// durable store without holding the in-memory lock, then publishes the complete
// replacement in one short critical section.
func (c *conversationState) reloadHistory() error {
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	snapshot, err := c.store.Snapshot()
	if err != nil {
		return fmt.Errorf("app: reload Session snapshot: %w", err)
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		return fmt.Errorf("app: reload Session history: %w", err)
	}
	c.historyMu.Lock()
	c.history = history
	c.historyMu.Unlock()
	return nil
}
