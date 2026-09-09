package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultLSLimit = 500
	lsSchema       = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Directory to list (default: current directory)"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 500, "description": "Maximum number of entries to return (default and hard maximum: 500). Use find to filter names or bash to inspect larger directories in batches."}
  },
  "additionalProperties": false
}`
)

// LS lists one directory.
type LS struct {
	workspace *Workspace
}

// NewLS constructs an ls tool.
func NewLS(workspace *Workspace) (*LS, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &LS{workspace: workspace}, nil
}

// Definition returns the model-facing ls contract.
func (l *LS) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "ls",
		Description:   "List files and directories, resolving relative paths from the working directory.",
		InputSchema:   jsonSchema(lsSchema),
		PromptSnippet: "List directory contents",
	}
}

// Execute returns a sorted, bounded directory listing.
func (l *LS) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	args, err := decodeArguments[arguments](ctx, call, "ls")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Limit < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"ls\": limit cannot be negative")
	}
	if args.Limit > defaultLSLimit {
		return llm.ToolResult{}, fmt.Errorf(
			"tool \"ls\": limit cannot exceed %d; use find to filter names or bash to inspect the directory in batches",
			defaultLSLimit,
		)
	}
	if args.Limit == 0 {
		args.Limit = defaultLSLimit
	}

	path, err := l.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"ls\": %w", err)
	}
	directory, err := os.Open(path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"ls\": open %q: %w", args.Path, err)
	}
	defer directory.Close()

	entries, err := directory.ReadDir(args.Limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return llm.ToolResult{}, fmt.Errorf("tool \"ls\": list %q: %w", args.Path, err)
	}
	slices.SortFunc(entries, func(left, right fs.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	entryNotice := ""
	if len(entries) > args.Limit {
		if args.Limit < defaultLSLimit {
			entryNotice = fmt.Sprintf("[entry limit reached: %d; retry with limit=%d (maximum %d)]\n",
				args.Limit, min(args.Limit*2, defaultLSLimit), defaultLSLimit)
		} else {
			entryNotice = fmt.Sprintf("[entry limit reached: %d (hard maximum); use find to filter names or bash to inspect the directory in batches]\n",
				args.Limit)
		}
	}
	const byteNotice = "[output truncated: 50 KiB limit reached; use find to filter names or bash to inspect the directory in batches]\n"
	// Reserve notices before adding entries so neither names nor notices are split.
	entryBudget := maxOutputBytes - len(entryNotice) - len(byteNotice)
	var output strings.Builder
	for index, entry := range entries {
		if index >= args.Limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return llm.ToolResult{}, err
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		} else if entry.Type()&fs.ModeSymlink != 0 {
			name += "@"
		}
		if output.Len()+len(name)+1 > entryBudget {
			output.WriteString(byteNotice)
			break
		}
		output.WriteString(name + "\n")
	}
	output.WriteString(entryNotice)
	return textResult(call, strings.TrimSuffix(output.String(), "\n"), false), nil
}
