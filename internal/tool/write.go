package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return llm.ToolResult{}, fmt.Errorf("tool \"write\": %q is not a regular file", args.Path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": stat %q: %w", args.Path, statErr)
	}
	if err := w.workspace.atomicWrite(ctx, path, []byte(content), mode); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"write\": write %q: %w", args.Path, err)
	}
	return textResult(call, fmt.Sprintf("Wrote %d bytes to %s.", len(content), args.Path), false), nil
}

// ResolvePath returns the physical write destination without creating anything.
// Existing symlinks must resolve completely; only genuinely missing components
// may be appended for a new file. Guard uses the same resolver as execution.
func (w *Write) ResolvePath(input string) (string, error) {
	path, err := w.workspace.resolvePath(input)
	if err != nil {
		return "", err
	}
	if os.IsPathSeparator(path[len(path)-1]) {
		return "", fmt.Errorf("resolve write path %q: file path ends with a separator", input)
	}
	volume := filepath.VolumeName(path)
	current := volume + string(os.PathSeparator)
	parts := strings.FieldsFunc(path[len(volume):], func(r rune) bool {
		return r == '/' || (os.PathSeparator == '\\' && r == '\\')
	})
	for index, part := range parts {
		// Keep traversal until existing symlinks have been resolved. Cleaning the
		// original path first would give link/../file the wrong destination.
		candidate := current + string(os.PathSeparator) + part
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			for _, remaining := range parts[index:] {
				if remaining == "." || remaining == ".." {
					return "", fmt.Errorf("resolve write path %q: traversal through a missing directory", input)
				}
			}
			return filepath.Join(append([]string{current}, parts[index:]...)...), nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve write path %q: %w", input, err)
		}
		current = candidate
		if info.Mode()&os.ModeSymlink != 0 {
			current, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve write symlink %q: %w", input, err)
			}
			info, err = os.Stat(current)
			if err != nil {
				return "", fmt.Errorf("inspect write target %q: %w", input, err)
			}
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("resolve write path %q: parent is not a directory", input)
		}
	}
	return filepath.Clean(current), nil
}
