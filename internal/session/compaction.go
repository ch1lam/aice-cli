package session

import (
	"errors"
	"fmt"
	"math"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// DefaultKeepRecentTokens is the approximate recent context retained after a
// manual compaction.
const DefaultKeepRecentTokens int64 = 20_000

// ErrNothingToCompact indicates that no older complete turn can be summarized
// while preserving the requested recent context.
var ErrNothingToCompact = errors.New("session has nothing to compact")

// CompactionSettings controls the complete-turn cut selected for a checkpoint.
type CompactionSettings struct {
	KeepRecentTokens int64
}

// CompactionPreparation is immutable input for generating a summary.
type CompactionPreparation struct {
	MessagesToSummarize []llm.AgentMessage
	TokensBefore        int64
	FirstKeptTurnID     string
	ActiveTurnCount     int
	RetainedTurnCount   int
}

// BuildContext derives the active model transcript from immutable source turns
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
			state.turns,
		)
		if err != nil {
			return nil, err
		}
		contextMessages = append(contextMessages, summary)
	}
	for _, turn := range state.turns {
		contextMessages = append(contextMessages, turn.Messages...)
	}
	return cloneMessages(contextMessages)
}

// PrepareCompaction selects a complete-turn cut on the active branch while
// retaining approximately KeepRecentTokens of its newest source history.
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
	if estimate.Tokens <= 0 || len(state.turns) < 2 {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	firstKept := 0
	var retainedTokens int64
	for turnIndex := len(state.turns) - 1; turnIndex >= 0; turnIndex-- {
		turnTokens, err := estimateTurnTokens(state.turns[turnIndex])
		if err != nil {
			return CompactionPreparation{}, fmt.Errorf(
				"session: estimate turn %q: %w",
				state.turns[turnIndex].ID,
				err,
			)
		}
		retainedTokens += turnTokens
		if retainedTokens >= settings.KeepRecentTokens {
			firstKept = turnIndex
			break
		}
	}
	if firstKept <= 0 {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	messagesToSummarize := make([]llm.AgentMessage, 0)
	if state.compaction != nil {
		summary, err := compactionSummaryMessage(
			*state.compaction,
			state.turns[:firstKept],
		)
		if err != nil {
			return CompactionPreparation{}, err
		}
		messagesToSummarize = append(messagesToSummarize, summary)
	}
	for turnIndex := 0; turnIndex < firstKept; turnIndex++ {
		messagesToSummarize = append(
			messagesToSummarize,
			state.turns[turnIndex].Messages...,
		)
	}
	cloned, err := cloneMessages(messagesToSummarize)
	if err != nil {
		return CompactionPreparation{}, err
	}
	return CompactionPreparation{
		MessagesToSummarize: cloned,
		TokensBefore:        estimate.Tokens,
		FirstKeptTurnID:     state.turns[firstKept].ID,
		ActiveTurnCount:     len(state.turns),
		RetainedTurnCount:   len(state.turns) - firstKept,
	}, nil
}

type activeBranchState struct {
	compaction *Compaction
	turns      []Turn
}

func deriveActiveBranch(snapshot Snapshot) (activeBranchState, error) {
	index, err := indexSnapshot(snapshot)
	if err != nil {
		return activeBranchState{}, err
	}
	nodes, err := ActiveBranch(snapshot)
	if err != nil {
		return activeBranchState{}, err
	}
	ids := make([]string, len(nodes))
	for position, node := range nodes {
		ids[position] = node.ID
	}
	turnIDs, err := activeTurnIDs(ids, index.nodeTypes, index.compactions)
	if err != nil {
		return activeBranchState{}, err
	}
	latestCompaction := -1
	for position, node := range nodes {
		if node.Type == RecordTypeCompaction {
			latestCompaction = position
		}
	}
	var checkpoint *Compaction
	if latestCompaction >= 0 {
		compaction := index.compactions[nodes[latestCompaction].ID]
		checkpoint = &compaction
	}
	turns := make([]Turn, 0, len(turnIDs))
	for _, id := range turnIDs {
		turns = append(turns, index.turns[id])
	}
	return activeBranchState{
		compaction: checkpoint,
		turns:      turns,
	}, nil
}

func compactionSummaryMessage(
	compaction Compaction,
	retainedTurns []Turn,
) (llm.CompactionSummaryMessage, error) {
	timestamp := compaction.CreatedAt
	for _, turn := range retainedTurns {
		for _, message := range turn.Messages {
			messageTime := agentMessageTimestamp(message)
			if messageTime < timestamp {
				continue
			}
			if messageTime == math.MaxInt64 {
				return llm.CompactionSummaryMessage{}, fmt.Errorf(
					"session: cannot order compaction summary after maximum timestamp",
				)
			}
			timestamp = messageTime + 1
		}
	}
	message := llm.CompactionSummaryMessage{
		Role:         llm.RoleCompactionSummary,
		Summary:      compaction.Summary,
		TokensBefore: compaction.TokensBefore,
		Timestamp:    timestamp,
	}
	if err := message.Validate(); err != nil {
		return llm.CompactionSummaryMessage{}, fmt.Errorf(
			"session: build compaction summary: %w",
			err,
		)
	}
	return message, nil
}

func estimateTurnTokens(turn Turn) (int64, error) {
	if err := turn.Validate(); err != nil {
		return 0, err
	}
	messages, err := llm.AgentMessagesToMessages(turn.Messages)
	if err != nil {
		return 0, err
	}
	var tokens int64
	for _, message := range messages {
		tokens += llm.EstimateMessageTokens(message)
	}
	return tokens, nil
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
