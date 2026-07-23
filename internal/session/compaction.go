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
	FirstKeptTurn       int
	TurnCount           int
}

// BuildContext derives the active model transcript from immutable source turns
// and the latest append-only compaction checkpoint.
func BuildContext(snapshot Snapshot) ([]llm.AgentMessage, error) {
	if err := validateSnapshotCompactions(snapshot); err != nil {
		return nil, err
	}

	start := 0
	contextMessages := make([]llm.AgentMessage, 0)
	if len(snapshot.Compactions) > 0 {
		latest := snapshot.Compactions[len(snapshot.Compactions)-1]
		start = latest.FirstKeptTurn
		summary, err := compactionSummaryMessage(latest, snapshot.Turns[start:])
		if err != nil {
			return nil, err
		}
		contextMessages = append(contextMessages, summary)
	}
	for turnIndex := start; turnIndex < len(snapshot.Turns); turnIndex++ {
		turn := snapshot.Turns[turnIndex]
		if err := turn.Validate(); err != nil {
			return nil, fmt.Errorf(
				"session: context turn %d: %w",
				turnIndex,
				err,
			)
		}
		contextMessages = append(contextMessages, turn.Messages...)
	}
	return cloneMessages(contextMessages)
}

// PrepareCompaction selects a complete-turn cut while retaining approximately
// KeepRecentTokens of the newest source history.
func PrepareCompaction(
	snapshot Snapshot,
	settings CompactionSettings,
) (CompactionPreparation, error) {
	if settings.KeepRecentTokens <= 0 {
		return CompactionPreparation{}, fmt.Errorf(
			"session: keep recent tokens must be positive",
		)
	}
	if err := validateSnapshotCompactions(snapshot); err != nil {
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
	if estimate.Tokens <= 0 {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	start := 0
	if len(snapshot.Compactions) > 0 {
		start = snapshot.Compactions[len(snapshot.Compactions)-1].FirstKeptTurn
	}
	firstKept := start
	var retainedTokens int64
	for turnIndex := len(snapshot.Turns) - 1; turnIndex >= start; turnIndex-- {
		turnTokens, err := estimateTurnTokens(snapshot.Turns[turnIndex])
		if err != nil {
			return CompactionPreparation{}, fmt.Errorf(
				"session: estimate turn %d: %w",
				turnIndex,
				err,
			)
		}
		retainedTokens += turnTokens
		if retainedTokens >= settings.KeepRecentTokens {
			firstKept = turnIndex
			break
		}
	}
	if firstKept <= start {
		return CompactionPreparation{}, ErrNothingToCompact
	}

	messagesToSummarize := make([]llm.AgentMessage, 0)
	if len(snapshot.Compactions) > 0 {
		latest := snapshot.Compactions[len(snapshot.Compactions)-1]
		summary, err := compactionSummaryMessage(
			latest,
			snapshot.Turns[start:firstKept],
		)
		if err != nil {
			return CompactionPreparation{}, err
		}
		messagesToSummarize = append(messagesToSummarize, summary)
	}
	for turnIndex := start; turnIndex < firstKept; turnIndex++ {
		turn := snapshot.Turns[turnIndex]
		if err := turn.Validate(); err != nil {
			return CompactionPreparation{}, fmt.Errorf(
				"session: compact turn %d: %w",
				turnIndex,
				err,
			)
		}
		messagesToSummarize = append(messagesToSummarize, turn.Messages...)
	}
	cloned, err := cloneMessages(messagesToSummarize)
	if err != nil {
		return CompactionPreparation{}, err
	}
	return CompactionPreparation{
		MessagesToSummarize: cloned,
		TokensBefore:        estimate.Tokens,
		FirstKeptTurn:       firstKept,
		TurnCount:           len(snapshot.Turns),
	}, nil
}

func validateSnapshotCompactions(snapshot Snapshot) error {
	for index, compaction := range snapshot.Compactions {
		if err := compaction.Validate(); err != nil {
			return fmt.Errorf("session: compaction %d: %w", index, err)
		}
		if compaction.TurnCount > len(snapshot.Turns) {
			return fmt.Errorf(
				"session: compaction %d turn count %d exceeds snapshot turn count %d",
				index,
				compaction.TurnCount,
				len(snapshot.Turns),
			)
		}
		if index == 0 {
			continue
		}
		previous := snapshot.Compactions[index-1]
		if compaction.FirstKeptTurn <= previous.FirstKeptTurn ||
			compaction.TurnCount <= previous.TurnCount {
			return fmt.Errorf(
				"session: compaction %d does not advance the derived context boundary",
				index,
			)
		}
	}
	return nil
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
