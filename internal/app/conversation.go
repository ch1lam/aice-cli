package app

import (
	"context"
	"errors"
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
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	c.historyMu.RLock()
	active := c.activeMainRun != nil
	c.historyMu.RUnlock()
	if active {
		return nil, nil, fmt.Errorf("app: another main run is active")
	}
	if c.store != nil {
		snapshot, err := c.store.Snapshot()
		if err != nil {
			return nil, nil, err
		}
		if _, err := session.BuildContext(snapshot); err != nil {
			return nil, nil, fmt.Errorf("app: Session cannot continue; reopen it to recover interrupted tool results or use /new: %w", err)
		}
	}

	// historySyncMu serializes new owners and transcript changes while the
	// expensive store validation above runs without blocking side snapshots.
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
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

// recordMessage publishes only replay-safe history. The store owns tool pairing:
// a partially completed tool group is durable but remains absent from side views.
func (c *conversationState) recordMessage(ctx context.Context, state *mainRunState, message llm.AgentMessage) error {
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	if err := appendSessionMessage(ctx, c.store, message); err != nil {
		return err
	}
	snapshot, err := c.store.Snapshot()
	if err != nil {
		return err
	}
	history, err := sessionHistory(snapshot)
	if errors.Is(err, session.ErrIncompleteGroup) {
		return nil
	}
	if err != nil {
		return err
	}
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	c.history = history
	if c.activeMainRun == state {
		state.pendingMessages = nil
	}
	return nil
}

// sideSnapshot returns a deep clone of the committed parent history plus
// accepted user inputs and complete model/tool rounds from the current main
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
