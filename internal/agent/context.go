package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// prepareRequest runs only between paired model rounds. A retry reuses the
// already prepared history and must not rebuild it from durable failed attempts.
func (e *runExecution) prepareRequest(ctx context.Context, allowCompaction bool) (llm.Request, error) {
	request, err := e.checkedRequest(e.history)
	if err == nil || !errors.Is(err, ErrContextLimit) || e.input.Compactor == nil || !allowCompaction {
		return request, err
	}
	compacted, compactErr := e.input.Compactor(ctx, slices.Clone(e.history))
	if compactErr != nil {
		return llm.Request{}, errors.Join(err, fmt.Errorf("agent: compact complete history: %w", compactErr))
	}
	request, err = e.checkedRequest(compacted)
	if err != nil {
		return llm.Request{}, fmt.Errorf("agent: protect request after compaction: %w", err)
	}
	e.history = slices.Clone(compacted)
	return request, nil
}

func (e *runExecution) checkedRequest(history []llm.AgentMessage) (llm.Request, error) {
	request, err := e.requestForHistory(history)
	if err == nil {
		err = checkCompactionThreshold(request)
	}
	if err == nil {
		err = request.Validate()
	}
	return request, err
}

func (e *runExecution) requestForHistory(
	history []llm.AgentMessage,
) (llm.Request, error) {
	definitions := make([]llm.ToolDefinition, len(e.loop.definitions))
	for index, definition := range e.loop.definitions {
		definition.InputSchema = slices.Clone(definition.InputSchema)
		definitions[index] = definition
	}
	messages, err := llm.AgentMessagesToMessages(history)
	if err != nil {
		return llm.Request{}, fmt.Errorf("agent: project history: %w", err)
	}
	request := llm.Request{
		Model:        e.input.Model,
		SystemPrompt: e.input.SystemPrompt,
		Messages:     messages,
		Tools:        definitions,
		Options:      e.input.Options,
	}
	return protectRequestContext(request)
}

func protectRequestContext(request llm.Request) (llm.Request, error) {
	contextWindow := request.Model.ContextWindow
	if contextWindow <= 0 {
		return request, nil
	}

	_, safetyTokens := llm.ContextBudgets(contextWindow)
	estimate := llm.EstimateContextTokens(request)
	requestedMaxTokens := request.Options.MaxTokens
	if requestedMaxTokens == 0 {
		requestedMaxTokens = request.Model.MaxTokens
	}
	availableMaxTokens := contextWindow - estimate.Tokens - safetyTokens
	if availableMaxTokens <= 0 {
		return llm.Request{}, fmt.Errorf(
			"%w: estimated context is %d tokens in a %d-token window",
			ErrContextLimit,
			estimate.Tokens,
			contextWindow,
		)
	}
	if requestedMaxTokens > availableMaxTokens {
		request.Options.MaxTokens = availableMaxTokens
	}
	return request, nil
}

func checkCompactionThreshold(request llm.Request) error {
	contextWindow := request.Model.ContextWindow
	if contextWindow <= 0 {
		return nil
	}
	reserveTokens, _ := llm.ContextBudgets(contextWindow)
	estimate := llm.EstimateContextTokens(request)
	compactionThreshold := contextWindow - reserveTokens
	if estimate.Tokens <= compactionThreshold {
		return nil
	}
	return fmt.Errorf(
		"%w: estimated context is %d tokens, threshold is %d for a %d-token window; "+
			"reduce the prompt or compact the session",
		ErrContextLimit,
		estimate.Tokens,
		compactionThreshold,
		contextWindow,
	)
}
