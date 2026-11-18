package app

import (
	"context"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// runScope holds the per-run bindings shared by Print and interactive main
// runs: model Session identity, the MCP catalog, and stored result readback.
type runScope struct {
	mcp   mcpRunBinding
	store *session.Store
}

// bindRunScope binds the run's context. memory supplies results when no Store
// exists.
func bindRunScope(
	ctx context.Context,
	store *session.Store,
	mcp *mcpOwner,
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
	return withResultReader(ctx, store, memory), runScope{mcp: run, store: store}, nil
}

func (r runScope) close() { r.mcp.Close() }

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
		RequestRecorder:  runConfigRecorder(r.store),
	}
}

// runConfigRecorder persists each distinct request configuration before the
// call it governs, so every assistant message can be rebuilt from the Session.
func runConfigRecorder(store *session.Store) agent.RequestRecorder {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, config llm.RequestConfig) (string, error) {
		record, err := session.NewRunConfig(buildinfo.Build(), config, time.Now().UnixMilli())
		if err != nil {
			return "", err
		}
		return record.ID, store.AppendRunConfig(ctx, record)
	}
}
