package app

import (
	"context"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// runScope holds the per-run bindings shared by Print and interactive main
// runs: model Session identity, Computer Use and MCP catalogs, and stored
// result readback. memory supplies results when no Store exists.
type runScope struct {
	mcp   mcpRunBinding
	close func() error
}

func bindRunScope(
	ctx context.Context,
	store *session.Store,
	mcp *mcpOwner,
	configuration config.Config,
	model llm.Model,
	memory func() []llm.AgentMessage,
) (context.Context, runScope, error) {
	ctx, err := modelSessionContext(ctx, store)
	if err != nil {
		return nil, runScope{}, err
	}
	ctx, run, err := mcp.bindRun(ctx, model)
	if err != nil {
		return nil, runScope{}, err
	}
	return withResultReader(ctx, store, memory), runScope{mcp: run, close: func() error {
		run.Close()
		return nil
	}}, nil
}

// input returns the RunInput fields every main run derives from its frozen
// settings and this scope.
func (r runScope) input(model llm.Model, systemPrompt string, options llm.StreamOptions, history []llm.AgentMessage, prompt llm.UserMessage) agent.RunInput {
	return agent.RunInput{
		ResultViewTokens: llm.ResultViewBudget(model.ContextWindow),
		Catalog:          r.mcp.Catalog(),
		PinnedTools:      r.mcp.pins,
		InitialTools:     r.mcp.initial,
		Model:            model,
		SystemPrompt:     systemPrompt + r.mcp.summary,
		History:          history,
		Prompt:           prompt,
		Options:          options,
	}
}
