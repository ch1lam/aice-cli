package tool_test

import (
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestEditDiffReflectsWrittenContent(t *testing.T) {
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "file", "a\r\nb\nc\r\n")
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
		"path": "file", "edits": []map[string]string{{"oldText": "a", "newText": "A"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "A\r\nb\r\nc\r\n" {
		t.Fatalf("written = %q, %v", data, err)
	}
	// The real newline normalization outside the requested replacement is visible.
	if !strings.Contains(result.Diff.Text, "-b\n") || !strings.Contains(result.Diff.Text, "+b\r\n") {
		t.Fatalf("diff = %q", result.Diff.Text)
	}
	if strings.Contains(resultText(t, result), "@@") {
		t.Fatal("diff leaked into model content")
	}
	failed, err := edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
		"path": "file", "edits": []map[string]string{{"oldText": "missing", "newText": "new"}},
	}))
	if err == nil || failed.Diff.Text != "" || failed.Diff.Truncated {
		t.Fatalf("failed result = %+v, %v", failed, err)
	}
}
