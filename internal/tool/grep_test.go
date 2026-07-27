package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestNewGrepRequiresWorkspace(t *testing.T) {
	t.Parallel()
	if _, err := tool.NewGrep(nil); err == nil {
		t.Fatal("NewGrep(nil) error = nil, want workspace error")
	}
}

func TestGrepDefinitionMatchesPiContract(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, _ := newWorkspace(t)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	definition := grep.Definition()
	if definition.Name != "grep" {
		t.Fatalf("Definition().Name = %q, want grep", definition.Name)
	}
	for _, expected := range []string{
		"Respects .gitignore",
		"100 matches",
		"50KB",
		"500 chars",
	} {
		if !strings.Contains(definition.Description, expected) {
			t.Fatalf("Definition().Description = %q, want %q", definition.Description, expected)
		}
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(InputSchema) error = %v", err)
	}
	wantProperties := []string{
		"context",
		"glob",
		"ignoreCase",
		"limit",
		"literal",
		"path",
		"pattern",
	}
	gotProperties := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		gotProperties = append(gotProperties, name)
	}
	slices.Sort(gotProperties)
	if !slices.Equal(gotProperties, wantProperties) {
		t.Fatalf("schema properties = %v, want %v", gotProperties, wantProperties)
	}
	if !slices.Equal(schema.Required, []string{"pattern"}) {
		t.Fatalf("schema required = %v, want [pattern]", schema.Required)
	}
}

func TestGrepExecuteSearchesWithPiCompatibleArguments(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a.go", "before\nNeedle value\nafter\n")
	writeFixture(t, root, "a.txt", "needle ignored\n")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern":    "needle",
		"glob":       "*.go",
		"ignoreCase": true,
		"literal":    true,
		"context":    1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "a.go-1- before\na.go:2: Needle value\na.go-3- after"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteIncludesHiddenFilesAndRespectsGitignore(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("os.Mkdir(.git) error = %v", err)
	}
	writeFixture(t, root, ".gitignore", "ignored.go\n")
	writeFixture(t, root, "visible.go", "search-marker\n")
	writeFixture(t, root, ".secret/hidden.go", "search-marker\n")
	writeFixture(t, root, "ignored.go", "search-marker\n")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(
		t.Context(),
		toolCall(t, "grep", map[string]any{"pattern": "search-marker"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := resultText(t, result)
	for _, expected := range []string{
		".secret/hidden.go:1: search-marker",
		"visible.go:1: search-marker",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Execute() text = %q, want %q", output, expected)
		}
	}
	if strings.Contains(output, "ignored.go") {
		t.Fatalf("Execute() text = %q, want ignored.go excluded", output)
	}
}

func TestGrepExecuteSearchesParentDirectory(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixture(t, parent, "outside.go", "needle\n")
	workspace := newWorkspaceAt(t, root)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "needle",
		"path":    "..",
		"glob":    "*.go",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "outside.go:1: needle"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteReturnsNoMatchesForBinaryFile(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "binary.dat", "needle\x00data")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(
		t.Context(),
		toolCall(t, "grep", map[string]any{"pattern": "needle"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "No matches found"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteEnforcesGlobalLimitWithContext(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(
		t,
		root,
		"context.txt",
		"before\nmatch one\nafter\nmiddle\nmatch two\nafter two\n",
	)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "match",
		"path":    "context.txt",
		"limit":   1,
		"context": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := strings.Join([]string{
		"context.txt-1- before",
		"context.txt:2: match one",
		"context.txt-3- after",
		"",
		"[1 matches limit reached. Use limit=2 for more, or refine pattern]",
	}, "\n")
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteTruncatesLongLinesToPiLimit(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "long.txt", strings.Repeat("界", 501)+"\n")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "界",
		"literal": true,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "long.txt:1: " + strings.Repeat("界", 500) + "... [truncated]\n\n" +
		"[Some lines truncated to 500 chars. Use read tool to see full lines]"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteTruncatesOutputAtCompleteLines(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	line := "needle" + strings.Repeat("x", 594)
	writeFixture(t, root, "large.txt", strings.Repeat(line+"\n", 120))
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "needle",
		"literal": true,
		"limit":   200,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := resultText(t, result)
	if len(output) > 50*1024 {
		t.Fatalf("Execute() output length = %d, want at most 50KB", len(output))
	}
	if !strings.Contains(
		output,
		"[50KB limit reached. Some lines truncated to 500 chars. Use read tool to see full lines]",
	) {
		t.Fatalf("Execute() text is missing truncation notice: %q", output)
	}
	matchOutput, _, found := strings.Cut(output, "\n\n[")
	if !found {
		t.Fatalf("Execute() text = %q, want separated notice", output)
	}
	for _, outputLine := range strings.Split(matchOutput, "\n") {
		if !strings.HasSuffix(outputLine, "... [truncated]") {
			t.Fatalf("Execute() contains partial output line %q", outputLine)
		}
	}
}

func TestGrepExecuteTreatsFlagLikePatternAsSearchText(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "target.txt", "target\n")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(
		t.Context(),
		toolCall(t, "grep", map[string]any{"pattern": "--help"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "No matches found"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteSurfacesRipgrepErrors(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, _ := newWorkspace(t)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	_, err = grep.Execute(
		t.Context(),
		toolCall(t, "grep", map[string]any{"pattern": "["}),
	)
	if err == nil || !strings.Contains(err.Error(), "regex parse error") {
		t.Fatalf("Execute() error = %v, want ripgrep regex error", err)
	}
}

func TestGrepExecuteHonorsCancellation(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, _ := newWorkspace(t)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = grep.Execute(ctx, toolCall(t, "grep", map[string]any{"pattern": "needle"}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
}

func TestNewGrepRequiresRipgrep(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace, _ := newWorkspace(t)
	_, err := tool.NewGrep(workspace)
	if err == nil || !strings.Contains(err.Error(), "find ripgrep (rg) executable") {
		t.Fatalf("NewGrep() error = %v, want missing ripgrep error", err)
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Fatalf("ripgrep runtime dependency is not available: %v", err)
	}
}
