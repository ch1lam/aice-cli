package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEditMatchingFailures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		edits   []replacement
		want    []string
	}{
		{
			name: "missing after valid edit", content: "alpha beta",
			edits: []replacement{{"alpha", "changed"}, {"missing", "new"}},
			want:  []string{"edits[1]", "not found", "reread", "whitespace", "line endings"},
		},
		{
			name: "duplicate after valid edit", content: "alpha same same same",
			edits: []replacement{{"alpha", "changed"}, {"same", "new"}},
			want:  []string{"edits[1]", "matched 3 times", "distinguishing context"},
		},
		{
			name: "self overlapping matches", content: "aaaa",
			edits: []replacement{{"aa", "new"}},
			want:  []string{"edits[0]", "matched 3 times", "distinguishing context"},
		},
		{
			name: "overlap retains input indices after sorting", content: "abcdef tail",
			edits: []replacement{{"tail", "end"}, {"cde", "x"}, {"abc", "y"}},
			want:  []string{"edits[2] and edits[1] overlap", "combine them into one edit"},
		},
		{
			name: "same range", content: "alpha beta",
			edits: []replacement{{"alpha", "a"}, {"alpha", "b"}},
			want:  []string{"edits[0]", "edits[1]", "overlap", "combine them into one edit"},
		},
		{
			name: "no cascading matches", content: "alpha beta",
			edits: []replacement{{"alpha", "created"}, {"created", "new"}},
			want:  []string{"edits[1]", "not found"},
		},
		{
			name: "whitespace remains exact", content: "one\ttwo\n",
			edits: []replacement{{"one two", "new"}},
			want:  []string{"edits[0]", "not found"},
		},
		{
			name: "unicode remains exact", content: "caf\u00e9\n",
			edits: []replacement{{"cafe\u0301", "new"}},
			want:  []string{"edits[0]", "not found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws, call, path := editMatchingFixture(t, tt.content, tt.edits)
			ws.mutationOps.open = func(string, int, os.FileMode) (mutationFile, error) {
				t.Fatal("invalid edit attempted a write")
				return nil, nil
			}
			edit, err := NewEdit(ws)
			if err != nil {
				t.Fatal(err)
			}
			result, err := edit.Execute(t.Context(), call)
			if err == nil {
				t.Fatalf("Execute() succeeded: %+v", result)
			}
			for _, want := range append(tt.want, `"matching.txt"`) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want %q", err, want)
				}
			}
			if len(result.Content) != 0 {
				t.Errorf("failure returned content: %+v", result)
			}
			assertMutationFiles(t, path, tt.content)
		})
	}
}

func editMatchingFixture(t *testing.T, content string, edits []replacement) (*Workspace, llm.ToolCall, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "matching.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]any{"path": "matching.txt", "edits": edits})
	if err != nil {
		t.Fatal(err)
	}
	return ws, llm.ToolCall{ID: "matching-1", Name: "edit", Arguments: arguments}, path
}
