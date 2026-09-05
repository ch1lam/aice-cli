package session

import (
	"context"
	"fmt"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// RecoverInterrupted completes only missing tool results on the active branch.
// Call after workspace verification. It never runs a tool or changes existing
// records, and each result is durable before the next is attempted.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	sequence, err := sequenceAt(s.index, s.leafID)
	if err != nil {
		return err
	}
	for _, call := range sequence.pending {
		const unknownOutcome = "Execution outcome unknown; this tool may have produced effects. " +
			"Inspect the current state before retrying."
		message, err := llm.NewToolResultMessage(llm.ToolResult{
			CallID: call.ID, Name: call.Name, IsError: true,
			Content: []llm.ContentPart{llm.NewTextContent(unknownOutcome).Part()},
		})
		if err != nil {
			return err
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		entry, err := NewMessage(id, s.leafID, time.Now().UnixMilli(), message)
		if err != nil {
			return err
		}
		if err := s.appendMessage(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}
