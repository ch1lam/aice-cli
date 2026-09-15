package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const writeSchema = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to write (relative or absolute)"},
    "content": {"type": "string", "description": "Complete final content of the file. Replaces all existing content; omitted old content is not preserved. Use edit for partial changes to an existing file."}
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
		Name: "write",
		Description: "Write complete content to a file. Creates the file if it doesn't exist, " +
			"overwrites it if it does, and automatically creates parent directories.",
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
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	args, err := decodeArguments[arguments](ctx, call, "write")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Content == nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": content is required and must be a string")
	}
	content := *args.Content
	if len(content) > maxMutationBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": content exceeds the 4 mib mutation limit")
	}
	w.workspace.mutationMu.Lock()
	defer w.workspace.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}

	path, err := w.ResolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": %w", err)
	}

	mode := os.FileMode(0o644)
	before, beforeKnown := "", true
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return llm.ToolResult{}, fmt.Errorf("tool \"write\": %q is not a regular file", args.Path)
		}
		mode = info.Mode().Perm()
		before, beforeKnown = writeDiffSource(path, info)
	} else if !os.IsNotExist(statErr) {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": stat %q: %w", args.Path, statErr)
	}
	if err := w.workspace.atomicWrite(ctx, path, []byte(content), mode); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": write %q: %w", args.Path, err)
	}
	result := textResult(call, fmt.Sprintf("Wrote %d bytes to %s.", len(content), args.Path), false)
	if beforeKnown && !strings.ContainsRune(content, 0) {
		result.Diff = editDiff(before, content)
	} else {
		result.Diff = llm.ToolDiff{Truncated: true}
	}
	return result, nil
}

// Reading the old file is best-effort display work: it must not prevent a
// permitted replacement of an unreadable, binary, or oversized file.
func writeDiffSource(path string, info os.FileInfo) (string, bool) {
	if info.Size() > maxMutationBytes {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMutationBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxMutationBytes || strings.ContainsRune(string(data), 0) {
		return "", false
	}
	return string(data), true
}

// ResolvePath returns the physical write destination without creating anything.
// Existing symlinks must resolve completely; only genuinely missing components
// may be appended for a new file. Guard uses the same resolver as execution.
func (w *Write) ResolvePath(input string) (string, error) {
	return w.workspace.resolveMutationPath(input)
}
