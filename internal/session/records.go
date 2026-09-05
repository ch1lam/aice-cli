// Package session persists AICE source messages as an
// append-only JSONL tree.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// RecordType identifies one physical JSONL record.
type RecordType string

const (
	// RecordTypeSession identifies the versioned first record.
	RecordTypeSession RecordType = "session"
	// RecordTypeMessage identifies one ended source message.
	RecordTypeMessage RecordType = "message"
	// RecordTypeCompaction identifies one derived context checkpoint.
	RecordTypeCompaction RecordType = "compaction"
	// RecordTypeLeaf identifies an append-only move of the active tree leaf.
	RecordTypeLeaf RecordType = "leaf"
)

// CurrentVersion is the session file format written by this build.
const CurrentVersion = 3

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

// MessageEntry stores one ended source message. A branch may temporarily end
// with outstanding tool calls; model context is derived only from paired groups.
type MessageEntry struct {
	Type      RecordType       `json:"-"`
	ID        string           `json:"-"`
	ParentID  string           `json:"-"`
	CreatedAt int64            `json:"-"`
	Message   llm.AgentMessage `json:"-"`
}

// CompactionInput contains caller-owned data for a derived checkpoint. An
// empty FirstKeptMessageID means the checkpoint summarizes the entire active
// branch and retains no source messages in the next model context; source messages
// remain in the append-only Session.
type CompactionInput struct {
	ID                   string
	ParentID             string
	CreatedAt            int64
	Summary              string
	TokensBefore         int64
	FirstKeptMessageID   string
	ActiveMessageCount   int
	RetainedMessageCount int
	Usage                llm.Usage
}

// Compaction is one append-only derived context checkpoint.
type Compaction struct {
	Type                 RecordType `json:"type"`
	ID                   string     `json:"id"`
	ParentID             string     `json:"parent_id"`
	CreatedAt            int64      `json:"created_at"`
	Summary              string     `json:"summary"`
	TokensBefore         int64      `json:"tokens_before"`
	FirstKeptMessageID   string     `json:"first_kept_message_id"`
	ActiveMessageCount   int        `json:"active_message_count"`
	RetainedMessageCount int        `json:"retained_message_count"`
	Usage                llm.Usage  `json:"usage"`
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
	Messages    []MessageEntry
	Compactions []Compaction
	LeafMoves   []Leaf
	Order       []string
	LeafID      string
}

// TotalUsage returns all model usage billed while producing the session,
// including abandoned branches and compaction summaries.
func TotalUsage(snapshot Snapshot) llm.Usage {
	var total llm.Usage
	for _, message := range snapshot.Messages {
		if assistant, ok := message.Message.(llm.AssistantMessage); ok {
			total = llm.AddUsage(total, assistant.Usage)
		}
	}
	for _, compaction := range snapshot.Compactions {
		total = llm.AddUsage(total, compaction.Usage)
	}
	return total
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
		Type:                 RecordTypeCompaction,
		ID:                   input.ID,
		ParentID:             input.ParentID,
		CreatedAt:            input.CreatedAt,
		Summary:              input.Summary,
		TokensBefore:         input.TokensBefore,
		FirstKeptMessageID:   input.FirstKeptMessageID,
		ActiveMessageCount:   input.ActiveMessageCount,
		RetainedMessageCount: input.RetainedMessageCount,
		Usage:                cloneUsage(input.Usage),
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
	if c.ActiveMessageCount <= 0 {
		return fmt.Errorf("session: compaction active message count must be positive")
	}
	if c.FirstKeptMessageID == "" {
		if c.RetainedMessageCount != 0 {
			return fmt.Errorf(
				"session: full compaction retained message count must be zero, got %d",
				c.RetainedMessageCount,
			)
		}
		return nil
	}
	if err := validateRecordID(
		"compaction first kept message",
		c.FirstKeptMessageID,
		false,
	); err != nil {
		return err
	}
	if c.RetainedMessageCount <= 0 ||
		c.RetainedMessageCount >= c.ActiveMessageCount {
		return fmt.Errorf(
			"session: compaction retained message count %d is outside active message count %d",
			c.RetainedMessageCount,
			c.ActiveMessageCount,
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

type messageJSON struct {
	Type      RecordType      `json:"type"`
	ID        string          `json:"id"`
	ParentID  string          `json:"parent_id,omitempty"`
	CreatedAt int64           `json:"created_at"`
	Message   json.RawMessage `json:"message"`
}

// NewMessage validates and defensively copies one source message.
func NewMessage(id, parentID string, createdAt int64, message llm.AgentMessage) (MessageEntry, error) {
	cloned, err := cloneMessages([]llm.AgentMessage{message})
	if err != nil {
		return MessageEntry{}, err
	}
	entry := MessageEntry{Type: RecordTypeMessage, ID: id, ParentID: parentID, CreatedAt: createdAt, Message: cloned[0]}
	if err := entry.Validate(); err != nil {
		return MessageEntry{}, err
	}
	return entry, nil
}

// Validate checks intrinsic fields; parent/tool pairing is checked by Store.
func (e MessageEntry) Validate() error {
	if e.Type != RecordTypeMessage {
		return fmt.Errorf("session: message has type %q", e.Type)
	}
	if err := validateRecordID("message", e.ID, false); err != nil {
		return err
	}
	if err := validateRecordID("message parent", e.ParentID, true); err != nil {
		return err
	}
	if e.CreatedAt <= 0 {
		return fmt.Errorf("session: message creation time must be positive")
	}
	switch e.Message.(type) {
	case llm.UserMessage, llm.AssistantMessage, llm.ToolResultMessage:
	default:
		return fmt.Errorf("session: source message has unsupported or derived type %T", e.Message)
	}
	if assistant, ok := e.Message.(llm.AssistantMessage); ok {
		switch assistant.StopReason {
		case llm.StopReasonStop, llm.StopReasonLength, llm.StopReasonToolUse,
			llm.StopReasonPause, llm.StopReasonRefusal, llm.StopReasonError, llm.StopReasonAborted:
		default:
			return fmt.Errorf("session: source assistant has no valid terminal stop reason %q", assistant.StopReason)
		}
	}
	if _, err := llm.MarshalAgentMessages([]llm.AgentMessage{e.Message}); err != nil {
		return fmt.Errorf("session: validate message: %w", err)
	}
	return nil
}

// MarshalJSON preserves the concrete source message as a single JSON object.
func (e MessageEntry) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	messages, err := llm.MarshalAgentMessages([]llm.AgentMessage{e.Message})
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(messages, &raw); err != nil {
		return nil, err
	}
	return json.Marshal(messageJSON{Type: e.Type, ID: e.ID, ParentID: e.ParentID, CreatedAt: e.CreatedAt, Message: raw[0]})
}

// UnmarshalJSON restores the source message, rejecting unknown record fields.
func (e *MessageEntry) UnmarshalJSON(data []byte) error {
	if e == nil {
		return fmt.Errorf("session: decode message into nil receiver")
	}
	var raw messageJSON
	if err := jsonutil.DecodeStrict(data, &raw); err != nil {
		return fmt.Errorf("session: decode message: %w", err)
	}
	array, err := json.Marshal([]json.RawMessage{raw.Message})
	if err != nil {
		return err
	}
	messages, err := llm.UnmarshalAgentMessages(array)
	if err != nil {
		return fmt.Errorf("session: decode source message: %w", err)
	}
	decoded := MessageEntry{Type: raw.Type, ID: raw.ID, ParentID: raw.ParentID, CreatedAt: raw.CreatedAt, Message: messages[0]}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*e = decoded
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

func cloneEntries(messages []MessageEntry) ([]MessageEntry, error) {
	cloned := make([]MessageEntry, len(messages))
	for index, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("session: clone message %d: %w", index, err)
		}
		if err := json.Unmarshal(data, &cloned[index]); err != nil {
			return nil, fmt.Errorf("session: clone message %d: %w", index, err)
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
