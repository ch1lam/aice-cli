package tool

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Unavailable is a tool stub for a capability whose external executable is
// missing at construction time. It keeps the built-in tool set identical
// across platforms while degrading gracefully: Definition still advertises the
// tool, and Execute returns a non-fatal error the agent loop feeds back to the
// model.
type Unavailable struct {
	name   string
	reason error
}

// NewUnavailable constructs an unavailable tool stub.
func NewUnavailable(name string, reason error) *Unavailable {
	if reason == nil {
		reason = errors.New("missing external dependency")
	}
	return &Unavailable{name: name, reason: reason}
}

// Definition returns the model-facing contract for an unavailable tool. The
// input schema is a valid empty object so tool registration succeeds.
func (u *Unavailable) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        u.name,
		Description: fmt.Sprintf("unavailable: %v", u.reason),
		InputSchema: jsonSchema(`{"type":"object"}`),
	}
}

// Execute reports that the tool cannot run.
func (u *Unavailable) Execute(_ context.Context, _ llm.ToolCall) (llm.ToolResult, error) {
	return llm.ToolResult{}, fmt.Errorf("tool %q unavailable: %w", u.name, u.reason)
}
