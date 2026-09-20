package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrNoProgress identifies a run stopped after repeated identical tool rounds.
var ErrNoProgress = errors.New("agent: repeated tool calls made no observable progress")

type repetitionTracker struct {
	fingerprint [sha256.Size]byte
	count       int
}

func (e *runExecution) checkProgress(round ModelRound) error {
	limit := e.loop.limits.NoProgress
	if limit == 0 {
		return nil
	}
	fingerprint, err := toolRoundFingerprint(round)
	if err != nil {
		return err
	}
	if e.repetition.count > 0 && e.repetition.fingerprint == fingerprint {
		e.repetition.count++
	} else {
		e.repetition = repetitionTracker{fingerprint: fingerprint, count: 1}
	}
	if e.repetition.count >= limit {
		return fmt.Errorf("%w for %d consecutive rounds; inspect the results or change the instruction before continuing (run-no-progress-limit=0 disables this check)", ErrNoProgress, limit)
	}
	return nil
}

// Compare observable tool work, not assistant prose, provider IDs or timestamps.
// Only a fixed-size digest is retained between rounds. Order is significant:
// AICE executes tool batches sequentially, so reordering can change behavior.
func toolRoundFingerprint(round ModelRound) ([sha256.Size]byte, error) {
	calls, err := extractToolCalls(round.Assistant)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if len(calls) != len(round.ToolResults) {
		return [sha256.Size]byte{}, fmt.Errorf("%w: cannot compare an unpaired tool round", ErrProtocol)
	}
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for index, call := range calls {
		arguments := bytes.TrimSpace(call.Arguments)
		// UseNumber preserves large integers; float64 would make distinct arguments
		// compare equal above 2^53. Encoding sorts object keys recursively.
		decoder := json.NewDecoder(bytes.NewReader(arguments))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err == nil {
			canonical, err := json.Marshal(value)
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			arguments = canonical
		}
		result := round.ToolResults[index]
		if err := encoder.Encode(struct {
			Name       string
			Arguments  string
			Content    []llm.ContentPart
			IsError    bool
			Diff       llm.ToolDiff
			Truncation llm.ToolTruncation
		}{call.Name, string(arguments), result.Content, result.IsError, result.Diff, result.Truncation}); err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("agent: compare tool round: %w", err)
		}
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, nil
}
