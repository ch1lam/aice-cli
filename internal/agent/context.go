package agent

import (
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	contextReserveTokens int64 = 16_384
	contextSafetyTokens  int64 = 4_096
)

func (e *runExecution) request() (llm.Request, error) {
	return e.requestForHistory(e.history)
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

	_, safetyTokens := contextBudgets(contextWindow)
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
	reserveTokens, _ := contextBudgets(contextWindow)
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

func contextBudgets(contextWindow int64) (int64, int64) {
	reserveTokens := contextReserveTokens
	if quarterWindow := contextWindow / 4; quarterWindow < reserveTokens {
		reserveTokens = max(quarterWindow, 1)
	}
	safetyTokens := contextSafetyTokens
	if quarterReserve := reserveTokens / 4; quarterReserve < safetyTokens {
		safetyTokens = max(quarterReserve, 1)
	}
	return reserveTokens, safetyTokens
}
