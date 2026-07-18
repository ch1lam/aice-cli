// Package agent defines the boundaries used by AICE's agent loop.
package agent

import (
	"context"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Model is the language model capability consumed by the agent loop.
type Model interface {
	Stream(ctx context.Context, request llm.Request) (llm.Stream, error)
}

// Tool is an executable capability supplied to the agent loop.
type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error)
}

// Limits bounds work performed by one agent run.
type Limits struct {
	MaxTurns     int
	MaxToolSteps int
}
