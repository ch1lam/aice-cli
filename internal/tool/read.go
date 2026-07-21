package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultReadLines = 2000
	readSchema       = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to read (relative or absolute)"},
    "offset": {"type": "integer", "minimum": 1, "description": "Line number to start reading from (1-indexed)"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of lines to read"}
  },
  "required": ["path"],
  "additionalProperties": false
}`
)

// Read reads bounded text content from one workspace file.
type Read struct {
	workspace *Workspace
}

// NewRead constructs a read tool.
func NewRead(workspace *Workspace) (*Read, error) {
	if workspace == nil || workspace.root == nil {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Read{workspace: workspace}, nil
}

// Definition returns the model-facing read contract.
func (r *Read) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "read",
		Description: "Read a text file from the workspace, optionally selecting a line range.",
		InputSchema: jsonSchema(readSchema),
	}
}

// Execute reads the requested file without allowing workspace escape.
func (r *Read) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	args, err := decodeArguments[arguments](ctx, call, "read")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Offset < 0 || args.Limit < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": offset and limit cannot be negative")
	}
	if args.Offset == 0 {
		args.Offset = 1
	}
	if args.Limit == 0 || args.Limit > defaultReadLines {
		args.Limit = defaultReadLines
	}

	path, err := r.workspace.resolvePath(args.Path, false)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %w", err)
	}
	file, err := r.workspace.root.Open(path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": open %q: %w", args.Path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": stat %q: %w", args.Path, err)
	}
	if !info.Mode().IsRegular() {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %q is not a regular file", args.Path)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxReadBytes+1))
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": read %q: %w", args.Path, err)
	}
	if len(data) > maxReadBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %q exceeds the 10 mib read limit", args.Path)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %q appears to be a binary file", args.Path)
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}

	if len(data) == 0 {
		return textResult(call, "", false), nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := args.Offset - 1
	if start >= len(lines) {
		return textResult(call, fmt.Sprintf("[offset %d is beyond end of file]", args.Offset), false), nil
	}
	end := min(start+args.Limit, len(lines))
	collector := newTextCollector(maxOutputBytes)
	collector.WriteString(strings.Join(lines[start:end], ""))
	if end < len(lines) && !collector.truncated {
		collector.WriteString(fmt.Sprintf("\n[showing lines %d-%d; more lines available]", start+1, end))
	}
	return textResult(call, collector.String(), false), nil
}
