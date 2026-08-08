package tool

import (
	"context"
	"fmt"
	"os"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const writeSchema = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to write (relative or absolute)"},
    "content": {"type": "string", "description": "Content to write to the file"}
  },
  "required": ["path", "content"],
  "additionalProperties": false
}`

// Write creates or replaces one file.
type Write struct {
	workspace *Workspace
}

// NewWrite constructs a write tool.
func NewWrite(workspace *Workspace) (*Write, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Write{workspace: workspace}, nil
}

// Definition returns the model-facing write contract.
func (w *Write) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "write",
		Description:   "Write complete content to a file, resolving relative paths from the working directory.",
		InputSchema:   jsonSchema(writeSchema),
		PromptSnippet: "Create or overwrite files",
		PromptGuidelines: []string{
			"Use write only for new files or complete rewrites.",
		},
	}
}

// Execute atomically creates or replaces the requested file.
func (w *Write) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	args, err := decodeArguments[arguments](ctx, call, "write")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if len(args.Content) > maxMutationBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": content exceeds the 4 mib mutation limit")
	}
	path, err := w.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": %w", err)
	}
	w.workspace.mutationMu.Lock()
	defer w.workspace.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return llm.ToolResult{}, fmt.Errorf("tool \"write\": %q is not a regular file", args.Path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": stat %q: %w", args.Path, statErr)
	}
	if err := w.workspace.atomicWrite(path, []byte(args.Content), mode); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": write %q: %w", args.Path, err)
	}
	return textResult(call, fmt.Sprintf("Wrote %d bytes to %s.", len(args.Content), args.Path), false), nil
}
