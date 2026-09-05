package agent

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// recordMessage is the single callback boundary for accepted source messages.
// The caller has already retained the message in Result or pendingInputs.
func (e *runExecution) recordMessage(ctx context.Context, message llm.AgentMessage) error {
	if e.recorderErr != nil {
		return e.recorderErr
	}
	if e.input.MessageRecorder != nil {
		data, err := llm.MarshalAgentMessages([]llm.AgentMessage{message})
		if err != nil {
			e.recorderErr = fmt.Errorf("agent: clone recorded message: %w", err)
			return e.recorderErr
		}
		cloned, err := llm.UnmarshalAgentMessages(data)
		if err != nil {
			e.recorderErr = fmt.Errorf("agent: clone recorded message: %w", err)
			return e.recorderErr
		}
		if err := e.input.MessageRecorder(ctx, cloned[0]); err != nil {
			e.recorderErr = fmt.Errorf("agent: record message: %w", err)
			return e.recorderErr
		}
	}
	e.recordedMessages++
	return nil
}
