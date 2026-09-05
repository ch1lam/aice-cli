package session

import (
	"errors"
	"fmt"
	"math"

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

// PrepareCompaction selects a paired-group cut on the active branch while
// retaining approximately KeepRecentTokens of its newest source history. If
// the newest paired group is itself larger than that budget, it falls back to
// summarizing the entire active branch so a long interaction cannot permanently block
// continuation.
func PrepareCompaction(
	snapshot Snapshot,
	settings CompactionSettings,
) (CompactionPreparation, error) {
	if settings.KeepRecentTokens <= 0 {
		return CompactionPreparation{}, fmt.Errorf(
			"session: keep recent tokens must be positive",
		)
	}
	state, err := deriveActiveBranch(snapshot)
	if err != nil {
		return CompactionPreparation{}, err
	}
	activeContext, err := BuildContext(snapshot)
	if err != nil {
		return CompactionPreparation{}, err
	}
	projected, err := llm.AgentMessagesToMessages(activeContext)
	if err != nil {
		return CompactionPreparation{}, fmt.Errorf(
			"session: project context for compaction: %w",
			err,
		)
	}
	estimate := llm.EstimateContextTokens(llm.Request{Messages: projected})
	if estimate.Tokens <= 0 || len(state.messages) == 0 {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	starts, groupTokens, err := pairedGroupTokens(state.messages)
	if err != nil {
		return CompactionPreparation{}, err
	}
	firstKept := 0
	var retainedTokens int64
	for group := len(starts) - 1; group >= 0; group-- {
		retainedTokens += groupTokens[group]
		if retainedTokens >= settings.KeepRecentTokens {
			firstKept = starts[group]
			break
		}
	}
	oversizedBudget := max(settings.KeepRecentTokens, minOversizedGroupTokens)
	if groupTokens[len(groupTokens)-1] >= oversizedBudget {
		return prepareFullCompaction(state, estimate.Tokens)
	}
	if firstKept <= 0 {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	messagesToSummarize := make([]llm.AgentMessage, 0)
	if state.compaction != nil {
		summary, err := compactionSummaryMessage(
			*state.compaction,
			state.messages[:firstKept],
		)
		if err != nil {
			return CompactionPreparation{}, err
		}
		messagesToSummarize = append(messagesToSummarize, summary)
	}
	for messageIndex := 0; messageIndex < firstKept; messageIndex++ {
		messagesToSummarize = append(
			messagesToSummarize,
			state.messages[messageIndex].Message,
		)
	}
	cloned, err := cloneMessages(messagesToSummarize)
	if err != nil {
		return CompactionPreparation{}, err
	}
	return CompactionPreparation{
		MessagesToSummarize:  cloned,
		TokensBefore:         estimate.Tokens,
		FirstKeptMessageID:   state.messages[firstKept].ID,
		ActiveMessageCount:   len(state.messages),
		RetainedMessageCount: len(state.messages) - firstKept,
	}, nil
}

func prepareFullCompaction(
	state activeBranchState,
	tokensBefore int64,
) (CompactionPreparation, error) {
	messagesToSummarize := make([]llm.AgentMessage, 0)
	if state.compaction != nil {
		summary, err := compactionSummaryMessage(*state.compaction, state.messages)
		if err != nil {
			return CompactionPreparation{}, err
		}
		messagesToSummarize = append(messagesToSummarize, summary)
	}
	for _, message := range state.messages {
		messagesToSummarize = append(messagesToSummarize, message.Message)
	}
	cloned, err := cloneMessages(messagesToSummarize)
	if err != nil {
		return CompactionPreparation{}, err
	}
	return CompactionPreparation{
		MessagesToSummarize:  cloned,
		TokensBefore:         tokensBefore,
		ActiveMessageCount:   len(state.messages),
		RetainedMessageCount: 0,
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
	timestamp := compaction.CreatedAt
	for _, entry := range retainedMessages {
		messageTime := agentMessageTimestamp(entry.Message)
		if messageTime < timestamp {
			continue
		}
		if messageTime == math.MaxInt64 {
			return llm.CompactionSummaryMessage{}, fmt.Errorf("session: cannot order compaction summary after maximum timestamp")
		}
		timestamp = messageTime + 1
	}

	message, err := llm.NewCompactionSummaryMessage(
		compaction.Summary,
		compaction.TokensBefore,
	)
	if err != nil {
		return llm.CompactionSummaryMessage{}, fmt.Errorf(
			"session: build compaction summary: %w",
			err,
		)
	}
	// Ordering on the derived transcript uses the last retained message,
	// not wall-clock time from the constructor.
	message.Timestamp = timestamp
	return message, nil
}

// pairedGroupTokens allows cuts between source messages except within an
// assistant's tool-call/result group. A retained suffix may start after a user.
func pairedGroupTokens(entries []MessageEntry) ([]int, []int64, error) {
	var starts []int
	var tokens []int64
	sequence := messageSequence{started: true}
	previousUser := false
	for position, entry := range entries {
		if position == 0 || (len(sequence.pending) == 0 && !previousUser) {
			starts = append(starts, position)
			tokens = append(tokens, 0)
		}
		_, previousUser = entry.Message.(llm.UserMessage)
		if err := sequence.accept(entry.Message); err != nil {
			return nil, nil, err
		}
		projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{entry.Message})
		if err != nil {
			return nil, nil, err
		}
		for _, message := range projected {
			tokens[len(tokens)-1] += llm.EstimateMessageTokens(message)
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
