package tool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestWriteRecordsCommittedDiff(t *testing.T) {
	for _, tt := range []struct {
		name, before, after string
		exists              bool
		added, removed      int
		unknown             bool
	}{
		{name: "create", after: "one\ntwo\n", added: 2},
		{name: "replace", exists: true, before: "keep\nold\n", after: "keep\nnew\nmore\n", added: 2, removed: 1},
		{name: "empty", exists: true, before: "old\n", removed: 1},
		{name: "unchanged", exists: true, before: "same\n", after: "same\n"},
		{name: "large diff", after: strings.Repeat("new\n", 3000), added: 3000},
		{name: "binary", exists: true, before: "\x00old", after: "new", unknown: true},
		{name: "oversized original", exists: true, before: strings.Repeat("x", maxMutationBytes+1), after: "new", unknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "target")
			if tt.exists {
				if err := os.WriteFile(path, []byte(tt.before), 0600); err != nil {
					t.Fatal(err)
				}
			}
			workspace, err := NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewWrite(workspace)
			if err != nil {
				t.Fatal(err)
			}
			args, err := json.Marshal(map[string]string{"path": "target", "content": tt.after})
			if err != nil {
				t.Fatal(err)
			}
			result, err := writer.Execute(t.Context(), llm.ToolCall{ID: "w1", Name: "write", Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if result.Diff.StatsKnown == tt.unknown || result.Diff.Added != tt.added || result.Diff.Removed != tt.removed {
				t.Fatalf("diff counts = %+v", result.Diff)
			}
			if (tt.unknown || tt.name == "large diff") && !result.Diff.Truncated {
				t.Fatal("missing incomplete display marker")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tt.after {
				t.Fatalf("committed file = %q, %v", data, err)
			}
			if strings.Contains(result.Content[0].Text, "@@") {
				t.Fatal("diff leaked into model content")
			}
		})
	}
}

func TestFailedWriteHasNoDiff(t *testing.T) {
	workspace, execute, _ := mutationTestTool(t, "write")
	failure := errors.New("rename failed")
	workspace.mutationOps.rename = func(string, string) error { return failure }
	result, err := execute(t.Context())
	if !errors.Is(err, failure) || result.Diff != (llm.ToolDiff{}) {
		t.Fatalf("failed write = %+v, %v", result, err)
	}
}
