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
      "description": "Replacements matched against the same original file, not the results of earlier entries. Ranges must not overlap or nest; combine overlapping changes into one entry.",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "oldText": {"type": "string", "description": "Non-empty exact text from the original file. Must match exactly once; include only enough context to make it unique."},
          "newText": {"type": "string", "description": "Replacement for the matched oldText. An empty string deletes that text."}
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
	index   int
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
		Description: "Edit an existing file using exact text replacements. " +
			"Each oldText must match a unique, non-overlapping region of the original file.",
		InputSchema:   jsonSchema(editSchema),
		PromptSnippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		PromptGuidelines: []string{
			"Use edit for precise changes (oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple edits instead of multiple edit calls",
			"Keep each oldText as small as possible while still being unique in the file",
			"Match all edits against the same original file; combine overlapping or nested changes into one entry",
			"If matching fails, reread the relevant content and correct oldText; add distinguishing context for multiple matches. Do not use write merely to bypass a matching failure.",
		},
	}
}

// Execute validates every replacement against the original content, then writes atomically.
func (e *Edit) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeEditArguments(ctx, call)
	if err != nil {
		return llm.ToolResult{}, err
	}

	e.workspace.mutationMu.Lock()
	defer e.workspace.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	path, err := e.ResolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %w", err)
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
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %q: %w", args.Path, err)
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	if updated == string(data) {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": %q: no changes; replacements leave the file unchanged; provide edits that change the content", args.Path)
	}
	if err := e.workspace.atomicWrite(ctx, path, []byte(updated), info.Mode().Perm()); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"edit\": write %q: %w", args.Path, err)
	}
	result := textResult(
		call,
		fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(args.Edits), args.Path),
		false,
	)
	result.Diff = editDiff(string(data), updated)
	return result, nil
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
		start := strings.Index(normalized, oldText)
		if start < 0 {
			return "", fmt.Errorf("edits[%d] oldText not found; reread the file and check whitespace and line endings; "+
				"correct oldText against the original content and retry edit, not write merely to bypass this failure", index)
		}
		count := 1
		for offset := start + 1; offset < len(normalized); {
			next := strings.Index(normalized[offset:], oldText)
			if next < 0 {
				break
			}
			count++
			offset += next + 1
		}
		if count != 1 {
			return "", fmt.Errorf("edits[%d] oldText matched %d times; reread the relevant content and "+
				"add distinguishing context so it matches exactly once; retry edit, not write merely to bypass this failure", index, count)
		}
		positioned = append(positioned, positionedReplacement{
			index:   index,
			start:   start,
			end:     start + len(oldText),
			newText: newText,
		})
	}
	sort.Slice(positioned, func(i, j int) bool { return positioned[i].start < positioned[j].start })
	for index := 1; index < len(positioned); index++ {
		if positioned[index].start < positioned[index-1].end {
			return "", fmt.Errorf("edits[%d] and edits[%d] overlap; combine them into one edit", positioned[index-1].index, positioned[index].index)
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

// ResolvePath returns the existing regular file that edit will replace.
// Guard uses the same physical destination and existence checks as execution.
func (e *Edit) ResolvePath(input string) (string, error) {
	path, err := e.workspace.resolveMutationPath(input)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect edit target %q: %w", input, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("edit target %q is not a regular file", input)
	}
	return path, nil
}
