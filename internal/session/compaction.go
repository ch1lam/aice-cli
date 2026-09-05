package session

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	// DefaultKeepRecentTokens is the approximate recent context retained after a
	// manual compaction.
	DefaultKeepRecentTokens int64 = 20_000
	// minOversizedGroupTokens avoids treating tiny configured budgets as a
	// reason to discard an otherwise useful paired group.
	minOversizedGroupTokens int64 = 8_192
)

// ErrNothingToCompact indicates that no source history can be safely
// summarized while preserving the requested recent context.
var ErrNothingToCompact = errors.New("session has nothing to compact")

// CompactionSettings controls the paired-group cut selected for a checkpoint.
type CompactionSettings struct {
	KeepRecentTokens int64
	// MaxRetainedTokens bounds automatic retention; zero keeps manual policy.
	MaxRetainedTokens int64
}

// CompactionPreparation is immutable input for generating a summary.
type CompactionPreparation struct {
	MessagesToSummarize  []llm.AgentMessage
	TokensBefore         int64
	FirstKeptMessageID   string
	ActiveMessageCount   int
	RetainedMessageCount int
}

// BuildContext derives the active model transcript from immutable source messages
// and the latest compaction checkpoint on the selected branch.
func BuildContext(snapshot Snapshot) ([]llm.AgentMessage, error) {
	state, err := deriveActiveBranch(snapshot)
	if err != nil {
		return nil, err
	}
	contextMessages := make([]llm.AgentMessage, 0)
	if state.compaction != nil {
		summary, err := compactionSummaryMessage(
			*state.compaction,
			state.messages,
		)
		if err != nil {
			return nil, err
		}
		contextMessages = append(contextMessages, summary)
	}
	for _, message := range state.messages {
		contextMessages = append(contextMessages, message.Message)
	}
	return cloneMessages(contextMessages)
}

// ContextCompactionPreparation is a pure cut of a complete current context.
type ContextCompactionPreparation struct {
	MessagesToSummarize []llm.AgentMessage
	RetainedMessages    []llm.AgentMessage
	FirstKeptIndex      int
	TokensBefore        int64
}

// PrepareContextCompaction preserves unanswered trailing users and never cuts
// within a tool-call/result group. It does not read or write a Session.
func PrepareContextCompaction(
	messages []llm.AgentMessage,
	settings CompactionSettings,
) (ContextCompactionPreparation, error) {
	if settings.KeepRecentTokens <= 0 || settings.MaxRetainedTokens < 0 {
		return ContextCompactionPreparation{}, fmt.Errorf("session: keep recent tokens must be positive and maximum nonnegative")
	}
	cloned, err := cloneMessages(messages)
	if err != nil {
		return ContextCompactionPreparation{}, err
	}
	offset := 0
	if len(cloned) > 0 {
		if _, ok := cloned[0].(llm.CompactionSummaryMessage); ok {
			offset = 1
		}
	}
	source := cloned[offset:]
	starts, tokens, err := pairedGroupTokens(source)
	if err != nil {
		return ContextCompactionPreparation{}, err
	}
	completed := len(source)
	for completed > 0 {
		if _, ok := source[completed-1].(llm.UserMessage); !ok {
			break
		}
		completed--
	}
	if completed == 0 {
		return ContextCompactionPreparation{}, ErrNothingToCompact
	}
	var protectedTokens int64
	if completed < len(source) {
		protectedTokens = tokens[len(tokens)-1]
		starts = starts[:len(starts)-1]
		tokens = tokens[:len(tokens)-1]
	}
	firstKept := 0
	var retainedTokens int64
	for group := len(starts) - 1; group >= 0; group-- {
		retainedTokens += tokens[group]
		if retainedTokens >= settings.KeepRecentTokens {
			firstKept = starts[group]
			break
		}
	}
	if settings.MaxRetainedTokens > 0 {
		// Select a suffix within the hard cap, including protected users. If those
		// users alone exceed it, preserve them and let full request validation fail.
		firstKept = completed
		retainedTokens = protectedTokens
		for group := len(starts) - 1; group >= 0; group-- {
			if retainedTokens+tokens[group] > settings.MaxRetainedTokens {
				break
			}
			retainedTokens += tokens[group]
			firstKept = starts[group]
			if retainedTokens-protectedTokens >= settings.KeepRecentTokens {
				break
			}
		}
	} else if tokens[len(tokens)-1] >= max(settings.KeepRecentTokens, minOversizedGroupTokens) {
		firstKept = completed
	}
	if firstKept <= 0 {
		return ContextCompactionPreparation{}, ErrNothingToCompact
	}
	projected, err := llm.AgentMessagesToMessages(cloned)
	if err != nil {
		return ContextCompactionPreparation{}, err
	}
	cut := offset + firstKept
	return ContextCompactionPreparation{
		MessagesToSummarize: cloned[:cut:cut],
		RetainedMessages:    cloned[cut:],
		FirstKeptIndex:      cut,
		TokensBefore:        llm.EstimateContextTokens(llm.Request{Messages: projected}).Tokens,
	}, nil
}

// PrepareCompaction maps the pure context cut to immutable source IDs.
func PrepareCompaction(
	snapshot Snapshot,
	settings CompactionSettings,
) (CompactionPreparation, error) {
	state, err := deriveActiveBranch(snapshot)
	if err != nil {
		return CompactionPreparation{}, err
	}
	history, err := BuildContext(snapshot)
	if err != nil {
		return CompactionPreparation{}, err
	}
	cut, err := PrepareContextCompaction(history, settings)
	if err != nil {
		return CompactionPreparation{}, err
	}
	firstKept := cut.FirstKeptIndex
	if state.compaction != nil {
		firstKept--
	}
	firstID := ""
	if firstKept < len(state.messages) {
		firstID = state.messages[firstKept].ID
	}
	return CompactionPreparation{
		MessagesToSummarize:  cut.MessagesToSummarize,
		TokensBefore:         cut.TokensBefore,
		FirstKeptMessageID:   firstID,
		ActiveMessageCount:   len(state.messages),
		RetainedMessageCount: len(state.messages) - firstKept,
	}, nil
}

type activeBranchState struct {
	compaction *Compaction
	messages   []MessageEntry
}

func deriveActiveBranch(snapshot Snapshot) (activeBranchState, error) {
	index, err := indexSnapshot(snapshot)
	if err != nil {
		return activeBranchState{}, err
	}
	if err := completeBoundary(index, snapshot.LeafID); err != nil {
		return activeBranchState{}, err
	}
	path, err := pathToRoot(snapshot.LeafID, index.nodeTypes, index.parents)
	if err != nil {
		return activeBranchState{}, err
	}
	ids, err := activeMessageIDs(path, index.nodeTypes, index.compactions)
	if err != nil {
		return activeBranchState{}, err
	}
	var checkpoint *Compaction
	for _, id := range path {
		if value, ok := index.compactions[id]; ok {
			checkpoint = &value
		}
	}
	messages := make([]MessageEntry, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, index.messages[id])
	}
	return activeBranchState{compaction: checkpoint, messages: messages}, nil
}

func compactionSummaryMessage(
	compaction Compaction,
	retainedMessages []MessageEntry,
) (llm.CompactionSummaryMessage, error) {
	retained := make([]llm.AgentMessage, 0, min(compaction.RetainedMessageCount, len(retainedMessages)))
	for _, entry := range retainedMessages[:min(compaction.RetainedMessageCount, len(retainedMessages))] {
		retained = append(retained, entry.Message)
	}
	return NewContextSummary(compaction.Summary, compaction.TokensBefore, compaction.CreatedAt, retained)
}

// NewContextSummary invalidates usage from the retained pre-compaction context.
// Later source messages may establish fresh usage without rewriting old records.
func NewContextSummary(
	summary string,
	tokensBefore, timestamp int64,
	retained []llm.AgentMessage,
) (llm.CompactionSummaryMessage, error) {
	for _, message := range retained {
		messageTime := agentMessageTimestamp(message)
		if messageTime < timestamp {
			continue
		}
		if messageTime == math.MaxInt64 {
			return llm.CompactionSummaryMessage{}, fmt.Errorf("session: cannot order compaction summary after maximum timestamp")
		}
		timestamp = messageTime + 1
	}

	message, err := llm.NewCompactionSummaryMessage(
		summary,
		tokensBefore,
	)
	if err != nil {
		return llm.CompactionSummaryMessage{}, fmt.Errorf(
			"session: build compaction summary: %w",
			err,
		)
	}
	// Invalidate usage from the checkpoint's retained prefix, while allowing
	// messages appended after the checkpoint to establish fresh usage.
	message.Timestamp = timestamp
	return message, nil
}

// pairedGroupTokens estimates the full projection once, so failed attempts
// superseded by a later assistant and their results contribute no model tokens.
func pairedGroupTokens(messages []llm.AgentMessage) ([]int, []int64, error) {
	projected, err := llm.AgentMessagesToMessages(messages)
	if err != nil {
		return nil, nil, err
	}
	var starts []int
	var tokens []int64
	sequence := messageSequence{started: true}
	previousUser := false
	projectedIndex := 0
	for position, message := range messages {
		if position == 0 || (len(sequence.pending) == 0 && !previousUser) {
			starts = append(starts, position)
			tokens = append(tokens, 0)
		}
		_, previousUser = message.(llm.UserMessage)
		if err := sequence.accept(message); err != nil {
			return nil, nil, err
		}
		if projectedIndex < len(projected) && reflect.DeepEqual(message, projected[projectedIndex]) {
			tokens[len(tokens)-1] += llm.EstimateMessageTokens(projected[projectedIndex])
			projectedIndex++
		}
	}
	if len(sequence.pending) != 0 {
		return nil, nil, ErrIncompleteGroup
	}
	return starts, tokens, nil
}

func agentMessageTimestamp(message llm.AgentMessage) int64 {
	switch value := message.(type) {
	case llm.UserMessage:
		return value.Timestamp
	case llm.AssistantMessage:
		return value.Timestamp
	case llm.ToolResultMessage:
		return value.Timestamp
	case llm.CompactionSummaryMessage:
		return value.Timestamp
	default:
		return 0
	}
}
