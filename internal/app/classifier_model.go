package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// classifierModel runs classifier prompts on a configured model through a
// tool-free Loop, as compaction does. It inherits the model's credentials and
// endpoint; the classifier sets the output budget and reasoning level.
// classifierReasoningTokens is the output budget when a model reasons
// although the classifier asked for less.
const classifierReasoningTokens = 4096

type classifierModel struct {
	configured configuredModel
}

func (m classifierModel) Complete(ctx context.Context, req classify.Completion) (string, llm.Usage, error) {
	prompt, err := llm.NewUserMessage(llm.NewTextContent(req.Prompt).Part())
	if err != nil {
		return "", llm.Usage{}, err
	}
	loop, err := agent.NewLoop(m.configured.service, nil, agent.WithRunLimits(agent.RunLimits{MaxTurns: 2}))
	if err != nil {
		return "", llm.Usage{}, err
	}
	options := m.configured.options
	options.MaxTokens = int64(req.MaxTokens)
	// Models that cannot lower reasoning to the requested level think anyway;
	// leave room for that before the short answer.
	options.Thinking = llm.ClampThinkingLevel(m.configured.model, req.Thinking)
	if options.Thinking != req.Thinking && options.Thinking != llm.ThinkingLevelOff {
		options.MaxTokens = max(options.MaxTokens, classifierReasoningTokens)
	}
	result, err := loop.Run(ctx, agent.RunInput{
		Model:        m.configured.model,
		SystemPrompt: req.System,
		Prompt:       prompt,
		Options:      options,
	}, nil)
	if err != nil {
		return "", result.Usage, err
	}
	if len(result.ModelRounds) == 0 {
		return "", result.Usage, fmt.Errorf("classifier model returned no response")
	}
	assistant := result.ModelRounds[len(result.ModelRounds)-1].Assistant
	if assistant.StopReason != llm.StopReasonStop {
		return "", result.Usage, fmt.Errorf("classifier model stopped with reason %q", assistant.StopReason)
	}
	return visibleText(assistant.Content, "\n"), result.Usage, nil
}
