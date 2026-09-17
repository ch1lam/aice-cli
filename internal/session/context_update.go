package session

import (
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ContextUpdate transfers independent messages to a derived-history owner.
// Replace means the owner must replace, rather than append to, its history.
// LeafID is the cursor for its next call to ContextSince.
type ContextUpdate struct {
	LeafID   string
	Messages []llm.AgentMessage
	Replace  bool
}

// ContextSince returns complete messages after a previously published leaf.
// Ordinary appends copy only the new group. A checkout to another branch or a
// new compaction returns a full replacement. An empty cursor starts at root.
// No messages are returned while the active branch has outstanding tool calls.
func (s *Store) ContextSince(after string) (ContextUpdate, error) {
	if s == nil {
		return ContextUpdate{}, fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := completeBoundary(s.index, s.leafID); err != nil {
		return ContextUpdate{}, err
	}
	if _, exists := s.index.nodeTypes[after]; exists {
		if err := completeBoundary(s.index, after); err != nil {
			return ContextUpdate{}, err
		}
	}
	messages := make([]llm.AgentMessage, 0)
	for id := s.leafID; id != after; {
		if id == "" || s.index.nodeTypes[id] == RecordTypeCompaction {
			// The Store owns and has validated these records. The pure snapshot
			// path remains the reference for rebuilding after structural changes;
			// BuildContext copies its result before the lock is released.
			messages, err := BuildContext(Snapshot{
				Header: s.header, Messages: s.messages, Compactions: s.compactions,
				LeafMoves: s.leafMoves, Order: s.order, LeafID: s.leafID,
			})
			return ContextUpdate{LeafID: s.leafID, Messages: messages, Replace: true}, err
		}
		entry := s.index.messages[id]
		messages = append(messages, entry.Message)
		id = entry.ParentID
	}
	slices.Reverse(messages)
	cloned, err := llm.CloneAgentMessages(messages)
	if err != nil {
		return ContextUpdate{}, err
	}
	return ContextUpdate{LeafID: s.leafID, Messages: cloned}, nil
}
