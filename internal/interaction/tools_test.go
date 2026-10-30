package interaction

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestToolKindComesFromNormalizedActions(t *testing.T) {
	t.Parallel()
	read := llm.ToolAction{Kind: "file", Op: "read", Path: "a.go"}
	write := llm.ToolAction{Kind: "file", Op: "write", Path: "b.go"}
	for _, tc := range []struct {
		name    string
		actions []llm.ToolAction
		want    ToolKind
	}{
		{"read", []llm.ToolAction{read}, ToolKindRead},
		{"grep", []llm.ToolAction{read}, ToolKindSearch},
		{"edit", []llm.ToolAction{write}, ToolKindEdit},
		{"bash", []llm.ToolAction{{Kind: "command", Command: "cat a.go > b.go"}, write, read}, ToolKindExecute},
		{"web_fetch", []llm.ToolAction{{Kind: "network", Target: "https://example.com"}}, ToolKindFetch},
		{"mcp__service__read", []llm.ToolAction{{Kind: "mcp", Target: "user:service"}}, ToolKindOther},
		// A self-described name never upgrades a call without actions.
		{"read", nil, ToolKindOther},
	} {
		if got := ToolKindOf(tc.name, tc.actions); got != tc.want {
			t.Errorf("ToolKindOf(%s, %v) = %q, want %q", tc.name, tc.actions, got, tc.want)
		}
	}
}

func TestToolLocationsAreAbsoluteAndDistinct(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "outside.txt")
	got := ToolLocations([]llm.ToolAction{
		{Kind: "command", Command: "cp a.go b.go"},
		{Kind: "file", Op: "write", Path: "b.go"},
		{Kind: "file", Op: "read", Path: "a.go"},
		{Kind: "file", Op: "read", Path: "b.go"},
		{Kind: "file", Op: "read", Path: absolute},
	}, workspace)
	want := []ToolLocation{{filepath.Join(workspace, "b.go")}, {filepath.Join(workspace, "a.go")}, {absolute}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %v, want %v", got, want)
	}
}
