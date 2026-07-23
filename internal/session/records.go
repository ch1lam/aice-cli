// Package session persists complete AICE conversation history as append-only JSONL.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// RecordType identifies one physical JSONL record.
type RecordType string

const (
	// RecordTypeSession identifies the versioned first record.
	RecordTypeSession RecordType = "session"
	// RecordTypeTurn identifies one complete agent run.
	RecordTypeTurn RecordType = "turn"
)

// CurrentVersion is the session file format written by this build.
const CurrentVersion = 1

// Metadata contains caller-owned values for a new session header.
type Metadata struct {
	ID               string
	CreatedAt        int64
	WorkingDirectory string
}

// Header is the versioned first record in every session file.
type Header struct {
	Type             RecordType `json:"type"`
	Version          int        `json:"version"`
	ID               string     `json:"id"`
	CreatedAt        int64      `json:"created_at"`
	WorkingDirectory string     `json:"working_directory"`
}

// Turn is one complete agent run persisted at a stable boundary.
type Turn struct {
	Type        RecordType         `json:"-"`
	CompletedAt int64              `json:"-"`
	Messages    []llm.AgentMessage `json:"-"`
	Usage       llm.Usage          `json:"-"`
}

// Snapshot is an independent copy of one loaded session.
type Snapshot struct {
	Header Header
	Turns  []Turn
}

type turnJSON struct {
	Type        RecordType      `json:"type"`
	CompletedAt int64           `json:"completed_at"`
	Messages    json.RawMessage `json:"messages"`
	Usage       llm.Usage       `json:"usage"`
}

// NewTurn validates and defensively copies a complete agent run.
func NewTurn(completedAt int64, messages []llm.AgentMessage) (Turn, error) {
	cloned, err := cloneMessages(messages)
	if err != nil {
		return Turn{}, err
	}
	turn := Turn{
		Type:        RecordTypeTurn,
		CompletedAt: completedAt,
		Messages:    cloned,
		Usage:       aggregateUsage(cloned),
	}
	if err := turn.Validate(); err != nil {
		return Turn{}, err
	}
	return turn, nil
}

// Validate checks that the turn ends at a replay-safe boundary.
func (t Turn) Validate() error {
	if t.Type != RecordTypeTurn {
		return fmt.Errorf("session: turn has type %q", t.Type)
	}
	if t.CompletedAt <= 0 {
		return fmt.Errorf("session: turn completion time must be positive")
	}
	if len(t.Messages) < 2 {
		return fmt.Errorf("session: turn must contain at least a user and assistant message")
	}
	if _, ok := t.Messages[0].(llm.UserMessage); !ok {
		return fmt.Errorf("session: turn must start with a user message")
	}
	if _, ok := t.Messages[len(t.Messages)-1].(llm.AssistantMessage); !ok {
		return fmt.Errorf("session: turn must end with an assistant message")
	}
	if _, err := llm.MarshalAgentMessages(t.Messages); err != nil {
		return fmt.Errorf("session: validate turn messages: %w", err)
	}

	type pendingCall struct {
		name string
	}
	pending := make(map[string]pendingCall)
	pendingOrder := make([]string, 0)
	seenCalls := make(map[string]struct{})
	for index, message := range t.Messages {
		switch value := message.(type) {
		case llm.UserMessage:
			if len(pending) > 0 {
				return fmt.Errorf(
					"session: turn message %d precedes results for tool calls",
					index,
				)
			}
		case llm.AssistantMessage:
			if len(pending) > 0 {
				return fmt.Errorf(
					"session: turn message %d precedes results for tool calls",
					index,
				)
			}
			for _, part := range value.Content {
				if part.Type != llm.ContentTypeToolCall {
					continue
				}
				call := part.ToolCall
				if _, exists := seenCalls[call.ID]; exists {
					return fmt.Errorf("session: duplicate tool call id %q", call.ID)
				}
				seenCalls[call.ID] = struct{}{}
				pending[call.ID] = pendingCall{name: call.Name}
				pendingOrder = append(pendingOrder, call.ID)
			}
		case llm.ToolResultMessage:
			call, exists := pending[value.ToolCallID]
			if !exists {
				return fmt.Errorf(
					"session: tool result %q has no pending tool call",
					value.ToolCallID,
				)
			}
			if value.ToolName != "" && value.ToolName != call.name {
				return fmt.Errorf(
					"session: tool result %q names %q, want %q",
					value.ToolCallID,
					value.ToolName,
					call.name,
				)
			}
			delete(pending, value.ToolCallID)
		case llm.CompactionSummaryMessage:
			return fmt.Errorf(
				"session: turn message %d is a derived message",
				index,
			)
		}
	}
	for _, callID := range pendingOrder {
		if _, exists := pending[callID]; exists {
			return fmt.Errorf("session: unpaired tool call %q", callID)
		}
	}

	expectedUsage := aggregateUsage(t.Messages)
	if !equalUsage(t.Usage, expectedUsage) {
		return fmt.Errorf("session: turn usage does not match assistant messages")
	}
	return nil
}

// MarshalJSON preserves concrete message types inside the turn record.
func (t Turn) MarshalJSON() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	messages, err := llm.MarshalAgentMessages(t.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: encode turn messages: %w", err)
	}
	data, err := json.Marshal(turnJSON{
		Type:        t.Type,
		CompletedAt: t.CompletedAt,
		Messages:    messages,
		Usage:       t.Usage,
	})
	if err != nil {
		return nil, fmt.Errorf("session: encode turn: %w", err)
	}
	return data, nil
}

// UnmarshalJSON restores concrete message types and validates the complete turn.
func (t *Turn) UnmarshalJSON(data []byte) error {
	if t == nil {
		return fmt.Errorf("session: decode turn into nil receiver")
	}
	var raw turnJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("session: decode turn: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("session: decode turn: multiple json values")
		}
		return fmt.Errorf("session: decode trailing turn data: %w", err)
	}
	messages, err := llm.UnmarshalAgentMessages(raw.Messages)
	if err != nil {
		return fmt.Errorf("session: decode turn messages: %w", err)
	}
	decoded := Turn{
		Type:        raw.Type,
		CompletedAt: raw.CompletedAt,
		Messages:    messages,
		Usage:       raw.Usage,
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*t = decoded
	return nil
}

func cloneMessages(messages []llm.AgentMessage) ([]llm.AgentMessage, error) {
	encoded, err := llm.MarshalAgentMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("session: clone messages: %w", err)
	}
	cloned, err := llm.UnmarshalAgentMessages(encoded)
	if err != nil {
		return nil, fmt.Errorf("session: clone messages: %w", err)
	}
	return cloned, nil
}

func cloneTurns(turns []Turn) ([]Turn, error) {
	cloned := make([]Turn, len(turns))
	for index, turn := range turns {
		data, err := json.Marshal(turn)
		if err != nil {
			return nil, fmt.Errorf("session: clone turn %d: %w", index, err)
		}
		if err := json.Unmarshal(data, &cloned[index]); err != nil {
			return nil, fmt.Errorf("session: clone turn %d: %w", index, err)
		}
	}
	return cloned, nil
}

func aggregateUsage(messages []llm.AgentMessage) llm.Usage {
	var total llm.Usage
	for _, message := range messages {
		assistant, ok := message.(llm.AssistantMessage)
		if !ok {
			continue
		}
		total.InputTokens += assistant.Usage.InputTokens
		total.OutputTokens += assistant.Usage.OutputTokens
		total.ReasoningTokens += assistant.Usage.ReasoningTokens
		total.CacheReadTokens += assistant.Usage.CacheReadTokens
		total.CacheWriteTokens += assistant.Usage.CacheWriteTokens
		total.TotalTokens += assistant.Usage.TotalTokens
		if assistant.Usage.Cost == nil {
			continue
		}
		if total.Cost == nil {
			total.Cost = &llm.Cost{}
		}
		total.Cost.Input += assistant.Usage.Cost.Input
		total.Cost.Output += assistant.Usage.Cost.Output
		total.Cost.CacheRead += assistant.Usage.Cost.CacheRead
		total.Cost.CacheWrite += assistant.Usage.Cost.CacheWrite
		total.Cost.Total += assistant.Usage.Cost.Total
	}
	return total
}

func equalUsage(left, right llm.Usage) bool {
	if left.InputTokens != right.InputTokens ||
		left.OutputTokens != right.OutputTokens ||
		left.ReasoningTokens != right.ReasoningTokens ||
		left.CacheReadTokens != right.CacheReadTokens ||
		left.CacheWriteTokens != right.CacheWriteTokens ||
		left.TotalTokens != right.TotalTokens {
		return false
	}
	if left.Cost == nil || right.Cost == nil {
		return left.Cost == nil && right.Cost == nil
	}
	return *left.Cost == *right.Cost
}
