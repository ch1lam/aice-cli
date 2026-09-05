package session

import (
	"errors"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrIncompleteGroup indicates that a branch ends before all tool results.
var ErrIncompleteGroup = errors.New("session has an incomplete tool group")

// messageSequence validates source order. Call IDs are scoped to one assistant
// response, so providers can reuse them in later completed model rounds.
type messageSequence struct {
	started bool
	pending []llm.ToolCall
}

func (s *messageSequence) accept(message llm.AgentMessage) error {
	if !s.started {
		if _, ok := message.(llm.UserMessage); !ok {
			return fmt.Errorf("session: branch must start with a user message")
		}
		s.started = true
	}
	switch value := message.(type) {
	case llm.UserMessage:
		if len(s.pending) != 0 {
			return ErrIncompleteGroup
		}
	case llm.AssistantMessage:
		if len(s.pending) != 0 {
			return ErrIncompleteGroup
		}
		seen := make(map[string]bool)
		for _, part := range value.Content {
			if part.Type != llm.ContentTypeToolCall {
				continue
			}
			call := *part.ToolCall
			if seen[call.ID] {
				return fmt.Errorf("session: duplicate tool call id %q in one group", call.ID)
			}
			seen[call.ID] = true
			s.pending = append(s.pending, call)
		}
	case llm.ToolResultMessage:
		position := slices.IndexFunc(s.pending, func(call llm.ToolCall) bool { return call.ID == value.ToolCallID })
		if position < 0 {
			return fmt.Errorf("session: tool result %q has no pending call", value.ToolCallID)
		}
		if value.ToolName != s.pending[position].Name {
			return fmt.Errorf("session: tool result %q names %q, want %q", value.ToolCallID, value.ToolName, s.pending[position].Name)
		}
		s.pending = slices.Delete(s.pending, position, position+1)
	default:
		return fmt.Errorf("session: invalid source message %T", message)
	}
	return nil
}

func sequenceAt(index recordIndex, leafID string) (messageSequence, error) {
	path, err := pathToRoot(leafID, index.nodeTypes, index.parents)
	if err != nil {
		return messageSequence{}, err
	}
	var sequence messageSequence
	for _, id := range path {
		if index.nodeTypes[id] == RecordTypeCompaction {
			if len(sequence.pending) != 0 {
				return messageSequence{}, ErrIncompleteGroup
			}
			continue
		}
		if err := sequence.accept(index.messages[id].Message); err != nil {
			return messageSequence{}, err
		}
	}
	return sequence, nil
}

func validateMessageAppend(index recordIndex, entry MessageEntry) error {
	sequence, err := sequenceAt(index, entry.ParentID)
	if err != nil {
		return err
	}
	return sequence.accept(entry.Message)
}

func completeBoundary(index recordIndex, leafID string) error {
	sequence, err := sequenceAt(index, leafID)
	if err != nil {
		return err
	}
	if len(sequence.pending) != 0 {
		return ErrIncompleteGroup
	}
	return nil
}
