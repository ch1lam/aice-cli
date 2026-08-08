package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const editSchema = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to edit (relative or absolute)"},
    "edits": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "oldText": {"type": "string", "description": "Exact text for one unique, non-overlapping replacement"},
          "newText": {"type": "string", "description": "Replacement text"}
        },
        "required": ["oldText", "newText"],
        "additionalProperties": false
      }
    }
  },
  "required": ["path", "edits"],
  "additionalProperties": false
}`

// Edit applies exact, non-overlapping replacements.
type Edit struct {
	workspace *Workspace
}

type replacement struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type positionedReplacement struct {
	start   int
	end     int
	newText string
}

// NewEdit constructs an edit tool.
func NewEdit(workspace *Workspace) (*Edit, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Edit{workspace: workspace}, nil
}

// Definition returns the model-facing edit contract.
func (e *Edit) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "edit",
		Description: "Edit one file using exact text replacements. Each oldText must " +
			"match once in the original file and replacements must not overlap.",
		InputSchema:   jsonSchema(editSchema),
		PromptSnippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		PromptGuidelines: []string{
			"Use edit for precise changes (oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple edits instead of multiple edit calls",
			"Keep each oldText as small as possible while still being unique in the file",
		},
	}
}

// Execute validates every replacement against the original content, then writes atomically.
func (e *Edit) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Path  string        `json:"path"`
		Edits []replacement `json:"edits"`
	}
	args, err := decodeArguments[arguments](ctx, call, "edit")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if len(args.Edits) == 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": edits must contain at least one replacement")
	}
	path, err := e.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %w", err)
	}

	e.workspace.mutationMu.Lock()
	defer e.workspace.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": open %q: %w", args.Path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": stat %q: %w", args.Path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %q is not a regular file", args.Path)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMutationBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": read %q: %w", args.Path, readErr)
	}
	if closeErr != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": close %q: %w", args.Path, closeErr)
	}
	if len(data) > maxMutationBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %q exceeds the 4 mib mutation limit", args.Path)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %q appears to be a binary file", args.Path)
	}

	updated, err := applyReplacements(string(data), args.Edits)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %w", err)
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	if err := e.workspace.atomicWrite(path, []byte(updated), info.Mode().Perm()); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": write %q: %w", args.Path, err)
	}
	return textResult(
		call,
		fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(args.Edits), args.Path),
		false,
	), nil
}

func applyReplacements(content string, edits []replacement) (string, error) {
	bom := ""
	if strings.HasPrefix(content, "\ufeff") {
		bom = "\ufeff"
		content = strings.TrimPrefix(content, bom)
	}
	usesCRLF := strings.Count(content, "\r\n") > strings.Count(content, "\n")-strings.Count(content, "\r\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")

	positioned := make([]positionedReplacement, 0, len(edits))
	for index, edit := range edits {
		oldText := strings.ReplaceAll(edit.OldText, "\r\n", "\n")
		newText := strings.ReplaceAll(edit.NewText, "\r\n", "\n")
		if oldText == "" {
			return "", fmt.Errorf("edit %d oldText is empty", index)
		}
		if count := strings.Count(normalized, oldText); count != 1 {
			return "", fmt.Errorf("edit %d oldText must match exactly once; matched %d times", index, count)
		}
		start := strings.Index(normalized, oldText)
		positioned = append(positioned, positionedReplacement{
			start:   start,
			end:     start + len(oldText),
			newText: newText,
		})
	}
	sort.Slice(positioned, func(i, j int) bool { return positioned[i].start < positioned[j].start })
	for index := 1; index < len(positioned); index++ {
		if positioned[index].start < positioned[index-1].end {
			return "", fmt.Errorf("edits %d and %d overlap", index-1, index)
		}
	}

	var builder strings.Builder
	last := 0
	for _, edit := range positioned {
		_, _ = builder.WriteString(normalized[last:edit.start])
		_, _ = builder.WriteString(edit.newText)
		last = edit.end
	}
	_, _ = builder.WriteString(normalized[last:])
	updated := builder.String()
	if usesCRLF {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	return bom + updated, nil
}
