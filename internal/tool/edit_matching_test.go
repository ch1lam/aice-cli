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
			name: "unchanged entry must match", content: "alpha beta",
			edits: []replacement{{"alpha", "changed"}, {"missing", "missing"}},
			want:  []string{"edits[1]", "not found"},
		},
		{
			name: "unchanged entry must be unique", content: "alpha same same",
			edits: []replacement{{"alpha", "changed"}, {"same", "same"}},
			want:  []string{"edits[1]", "matched 2 times"},
		},
		{
			name: "unchanged entry must not overlap", content: "alpha beta",
			edits: []replacement{{"alpha", "changed"}, {"alpha beta", "alpha beta"}},
			want:  []string{"edits[0]", "edits[1]", "overlap"},
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

func TestEditMatchingNoChanges(t *testing.T) {
	tests := []struct {
		name    string
		content string
		edits   []replacement
	}{
		{name: "single", content: "alpha", edits: []replacement{{"alpha", "alpha"}}},
		{name: "multiple", content: "alpha beta", edits: []replacement{{"beta", "beta"}, {"alpha", "alpha"}}},
		{name: "BOM and CRLF", content: "\ufeffone\r\ntwo\r\n", edits: []replacement{{"one\ntwo", "one\r\ntwo"}}},
		{name: "LF normalization", content: "one\ntwo\n", edits: []replacement{{"one\r\ntwo", "one\ntwo"}}},
		{name: "adjacent edits cancel out", content: "abc", edits: []replacement{{"c", "bc"}, {"ab", "a"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws, call, path := editMatchingFixture(t, tt.content, tt.edits)
			ws.mutationOps.open = func(string, int, os.FileMode) (mutationFile, error) {
				t.Fatal("unchanged edit attempted a write")
				return nil, nil
			}
			edit, err := NewEdit(ws)
			if err != nil {
				t.Fatal(err)
			}
			result, err := edit.Execute(t.Context(), call)
			if err == nil || !strings.Contains(err.Error(), `"matching.txt": no changes`) {
				t.Fatalf("Execute() = %+v, %v; want no changes error", result, err)
			}
			if len(result.Content) != 0 {
				t.Errorf("unchanged edit returned content: %+v", result)
			}
			assertMutationFiles(t, path, tt.content)
		})
	}
}

func TestEditMatchingMixedChanges(t *testing.T) {
	for _, unchangedFirst := range []bool{false, true} {
		name := "unchanged last"
		if unchangedFirst {
			name = "unchanged first"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			edits := []replacement{{"one", "first"}, {"two", "two"}}
			if unchangedFirst {
				edits[0], edits[1] = edits[1], edits[0]
			}
			ws, call, path := editMatchingFixture(t, "\ufeffone\r\ntwo\r\n", edits)
			open := ws.mutationOps.open
			writes := 0
			ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
				writes++
				return open(path, flags, mode)
			}
			edit, err := NewEdit(ws)
			if err != nil {
				t.Fatal(err)
			}
			result, err := edit.Execute(t.Context(), call)
			if err != nil || result.IsError || writes != 1 {
				t.Fatalf("Execute() = %+v, %v; writes = %d", result, err, writes)
			}
			if len(result.Content) != 1 || result.Content[0].Text != "Successfully replaced 2 block(s) in matching.txt." {
				t.Fatalf("result = %+v", result)
			}
			assertMutationFiles(t, path, "\ufefffirst\r\ntwo\r\n")
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
