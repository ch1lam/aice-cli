// Package session persists complete AICE conversation history as an
// append-only JSONL tree.
package session

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// RecordType identifies one physical JSONL record.
type RecordType string

const (
	// RecordTypeSession identifies the versioned first record.
	RecordTypeSession RecordType = "session"
	// RecordTypeTurn identifies one complete agent run.
	RecordTypeTurn RecordType = "turn"
	// RecordTypeCompaction identifies one derived context checkpoint.
	RecordTypeCompaction RecordType = "compaction"
	// RecordTypeLeaf identifies an append-only move of the active tree leaf.
	RecordTypeLeaf RecordType = "leaf"
)

// CurrentVersion is the session file format written by this build.
const CurrentVersion = 2

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

// Node is the relationship shared by records that participate in the
// conversation tree. Leaf records move the active pointer and are not nodes.
type Node struct {
	Type      RecordType
	ID        string
	ParentID  string
	Timestamp int64
}

// Turn is one complete agent run persisted at a stable boundary.
type Turn struct {
	Type        RecordType         `json:"-"`
	ID          string             `json:"-"`
	ParentID    string             `json:"-"`
	CompletedAt int64              `json:"-"`
	Messages    []llm.AgentMessage `json:"-"`
	Usage       llm.Usage          `json:"-"`
}

// CompactionInput contains caller-owned data for a derived checkpoint.
type CompactionInput struct {
	ID                string
	ParentID          string
	CreatedAt         int64
	Summary           string
	TokensBefore      int64
	FirstKeptTurnID   string
	ActiveTurnCount   int
	RetainedTurnCount int
	Usage             llm.Usage
}

// Compaction is one append-only derived context checkpoint.
type Compaction struct {
	Type              RecordType `json:"type"`
	ID                string     `json:"id"`
	ParentID          string     `json:"parent_id"`
	CreatedAt         int64      `json:"created_at"`
	Summary           string     `json:"summary"`
	TokensBefore      int64      `json:"tokens_before"`
	FirstKeptTurnID   string     `json:"first_kept_turn_id"`
	ActiveTurnCount   int        `json:"active_turn_count"`
	RetainedTurnCount int        `json:"retained_turn_count"`
	Usage             llm.Usage  `json:"usage"`
}

// Leaf is an append-only move of the active branch pointer. An empty TargetID
// represents the tree root.
type Leaf struct {
	Type      RecordType `json:"type"`
	ID        string     `json:"id"`
	ParentID  string     `json:"parent_id,omitempty"`
	CreatedAt int64      `json:"created_at"`
	TargetID  string     `json:"target_id,omitempty"`
}

// Snapshot is an independent copy of one loaded session.
type Snapshot struct {
	Header      Header
	Turns       []Turn
	Compactions []Compaction
	LeafMoves   []Leaf
	Order       []string
	LeafID      string
}

// NewID returns a cryptographically random identifier suitable for sessions
// and tree records.
func NewID() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("session: generate id: %w", err)
	}
	return hex.EncodeToString(entropy[:]), nil
}

// NewCompaction validates and defensively copies one derived checkpoint.
func NewCompaction(input CompactionInput) (Compaction, error) {
	compaction := Compaction{
		Type:              RecordTypeCompaction,
		ID:                input.ID,
		ParentID:          input.ParentID,
		CreatedAt:         input.CreatedAt,
		Summary:           input.Summary,
		TokensBefore:      input.TokensBefore,
		FirstKeptTurnID:   input.FirstKeptTurnID,
		ActiveTurnCount:   input.ActiveTurnCount,
		RetainedTurnCount: input.RetainedTurnCount,
		Usage:             cloneUsage(input.Usage),
	}
	if err := compaction.Validate(); err != nil {
		return Compaction{}, err
	}
	return compaction, nil
}

// Validate checks the intrinsic fields of a derived checkpoint.
func (c Compaction) Validate() error {
	if c.Type != RecordTypeCompaction {
		return fmt.Errorf("session: compaction has type %q", c.Type)
	}
	if err := validateRecordID("compaction", c.ID, false); err != nil {
		return err
	}
	if err := validateRecordID("compaction parent", c.ParentID, false); err != nil {
		return err
	}
	if c.CreatedAt <= 0 {
		return fmt.Errorf("session: compaction creation time must be positive")
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("session: compaction summary is required")
	}
	if c.TokensBefore <= 0 {
		return fmt.Errorf("session: compaction tokens before must be positive")
	}
	if err := validateRecordID(
		"compaction first kept turn",
		c.FirstKeptTurnID,
		false,
	); err != nil {
		return err
	}
	if c.ActiveTurnCount < 2 {
		return fmt.Errorf("session: compaction active turn count must be at least two")
	}
	if c.RetainedTurnCount <= 0 ||
		c.RetainedTurnCount >= c.ActiveTurnCount {
		return fmt.Errorf(
			"session: compaction retained turn count %d is outside active turn count %d",
			c.RetainedTurnCount,
			c.ActiveTurnCount,
		)
	}
	return nil
}

// NewLeaf validates one active-branch move record.
func NewLeaf(
	id string,
	parentID string,
	targetID string,
	createdAt int64,
) (Leaf, error) {
	leaf := Leaf{
		Type:      RecordTypeLeaf,
		ID:        id,
		ParentID:  parentID,
		CreatedAt: createdAt,
		TargetID:  targetID,
	}
	if err := leaf.Validate(); err != nil {
		return Leaf{}, err
	}
	return leaf, nil
}

// Validate checks the intrinsic fields of an active-branch move.
func (l Leaf) Validate() error {
	if l.Type != RecordTypeLeaf {
		return fmt.Errorf("session: leaf has type %q", l.Type)
	}
	if err := validateRecordID("leaf", l.ID, false); err != nil {
		return err
	}
	if err := validateRecordID("leaf parent", l.ParentID, true); err != nil {
		return err
	}
	if err := validateRecordID("leaf target", l.TargetID, true); err != nil {
		return err
	}
	if l.CreatedAt <= 0 {
		return fmt.Errorf("session: leaf creation time must be positive")
	}
	return nil
}

type turnJSON struct {
	Type        RecordType      `json:"type"`
	ID          string          `json:"id"`
	ParentID    string          `json:"parent_id,omitempty"`
	CompletedAt int64           `json:"completed_at"`
	Messages    json.RawMessage `json:"messages"`
	Usage       llm.Usage       `json:"usage"`
}

// NewTurn validates and defensively copies a complete agent run.
func NewTurn(
	id string,
	parentID string,
	completedAt int64,
	messages []llm.AgentMessage,
) (Turn, error) {
	cloned, err := cloneMessages(messages)
	if err != nil {
		return Turn{}, err
	}
	turn := Turn{
		Type:        RecordTypeTurn,
		ID:          id,
		ParentID:    parentID,
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
	if err := validateRecordID("turn", t.ID, false); err != nil {
		return err
	}
	if err := validateRecordID("turn parent", t.ParentID, true); err != nil {
		return err
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
		ID:          t.ID,
		ParentID:    t.ParentID,
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
		ID:          raw.ID,
		ParentID:    raw.ParentID,
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

func validateRecordID(label string, id string, allowEmpty bool) error {
	if strings.TrimSpace(id) == "" {
		if allowEmpty && id == "" {
			return nil
		}
		return fmt.Errorf("session: %s id is required", label)
	}
	if strings.IndexByte(id, 0) >= 0 {
		return fmt.Errorf("session: %s id contains a null byte", label)
	}
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

func cloneCompactions(compactions []Compaction) []Compaction {
	cloned := make([]Compaction, len(compactions))
	for index, compaction := range compactions {
		cloned[index] = compaction
		cloned[index].Usage = cloneUsage(compaction.Usage)
	}
	return cloned
}

func cloneLeaves(leaves []Leaf) []Leaf {
	return append([]Leaf(nil), leaves...)
}

func cloneUsage(usage llm.Usage) llm.Usage {
	cloned := usage
	if usage.Cost != nil {
		cost := *usage.Cost
		cloned.Cost = &cost
	}
	return cloned
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
