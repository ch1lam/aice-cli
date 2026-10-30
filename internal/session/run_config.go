package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrNoRunConfig reports an assistant message without a recorded configuration.
var ErrNoRunConfig = errors.New("session: message has no recorded request configuration")

// RunConfig is the effective configuration of model calls, content-addressed
// so each assistant message names the exact input it was requested with. It
// is metadata, not a conversation node: it has no parent, never moves the
// active branch, and is written once per distinct configuration.
type RunConfig struct {
	Type      RecordType `json:"type"`
	ID        string     `json:"id"`
	CreatedAt int64      `json:"created_at"`
	// Build is the AICE version that sent the requests.
	Build  string            `json:"build"`
	Config llm.RequestConfig `json:"config"`
}

// NewRunConfig derives the record ID from build and config.
func NewRunConfig(build string, config llm.RequestConfig, createdAt int64) (RunConfig, error) {
	id, err := runConfigID(build, config)
	if err != nil {
		return RunConfig{}, err
	}
	record := RunConfig{Type: RecordTypeRunConfig, ID: id, CreatedAt: createdAt, Build: build, Config: config}
	return record, record.Validate()
}

func runConfigID(build string, config llm.RequestConfig) (string, error) {
	data, err := json.Marshal(struct {
		Build  string            `json:"build"`
		Config llm.RequestConfig `json:"config"`
	}{build, config})
	if err != nil {
		return "", fmt.Errorf("session: encode run config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks that the ID is the digest of the recorded content.
func (r RunConfig) Validate() error {
	if r.Type != RecordTypeRunConfig {
		return fmt.Errorf("session: invalid run config record type %q", r.Type)
	}
	if r.CreatedAt <= 0 {
		return fmt.Errorf("session: run config creation time must be positive")
	}
	id, err := runConfigID(r.Build, r.Config)
	if err != nil {
		return err
	}
	if r.ID != id {
		return fmt.Errorf("session: run config id %q does not match its content", r.ID)
	}
	return nil
}

// AppendRunConfig durably records a configuration unless it is already
// recorded. It must complete before the model call it governs.
func (s *Store) AppendRunConfig(ctx context.Context, record RunConfig) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	if _, exists := s.index.runConfigs[record.ID]; exists {
		return nil
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if _, exists := s.index.recordIDs[record.ID]; exists {
		return fmt.Errorf("session: duplicate record id %q", record.ID)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("session: encode run config: %w", err)
	}
	if err := s.appendData(ctx, "run config", data); err != nil {
		return err
	}
	var stored RunConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("session: retain appended run config: %w", err)
	}
	s.retainRunConfig(stored)
	return nil
}

func (s *storeState) retainRunConfig(record RunConfig) {
	s.runConfigs = append(s.runConfigs, record)
	s.index.runConfigs[record.ID] = record
	s.index.recordIDs[record.ID] = struct{}{}
}

// RebuildRequest reconstructs the complete model request of one assistant
// message from the Session alone: its recorded configuration plus its context,
// derived by the same functions the Agent Loop uses. The parent context is
// projected with the message appended and its own projection then dropped,
// because a failed attempt is omitted exactly when a later assistant retries it.
func RebuildRequest(snapshot Snapshot, messageID string) (llm.Request, error) {
	index := slices.IndexFunc(snapshot.Messages, func(entry MessageEntry) bool { return entry.ID == messageID })
	if index < 0 {
		return llm.Request{}, fmt.Errorf("session: message %q is missing", messageID)
	}
	entry := snapshot.Messages[index]
	assistant, ok := entry.Message.(llm.AssistantMessage)
	if !ok {
		return llm.Request{}, fmt.Errorf("session: message %q is not an assistant message", messageID)
	}
	if assistant.RequestConfig == "" {
		return llm.Request{}, ErrNoRunConfig
	}
	config := slices.IndexFunc(snapshot.RunConfigs, func(record RunConfig) bool { return record.ID == assistant.RequestConfig })
	if config < 0 {
		return llm.Request{}, fmt.Errorf("session: run config %q is missing", assistant.RequestConfig)
	}
	parent := snapshot
	parent.LeafID = entry.ParentID
	history, err := BuildContext(parent)
	if err != nil {
		return llm.Request{}, err
	}
	request, _, err := llm.BuildRequest(snapshot.RunConfigs[config].Config, append(history, assistant))
	if err != nil {
		return llm.Request{}, err
	}
	last := len(request.Messages) - 1
	if last < 0 {
		return llm.Request{}, fmt.Errorf("session: message %q has no projected context", messageID)
	}
	if _, ok := request.Messages[last].(llm.AssistantMessage); !ok {
		return llm.Request{}, fmt.Errorf("session: message %q does not end its projected context", messageID)
	}
	request.Messages = request.Messages[:last]
	fitted, estimate, ok := llm.FitOutputTokens(request)
	if !ok {
		return llm.Request{}, fmt.Errorf("session: rebuilt context of %d tokens leaves no output budget", estimate)
	}
	return fitted, nil
}

func cloneRunConfigs(records []RunConfig) []RunConfig {
	cloned := make([]RunConfig, len(records))
	for i, record := range records {
		cloned[i] = record
		cloned[i].Config = record.Config.Clone()
	}
	return cloned
}
