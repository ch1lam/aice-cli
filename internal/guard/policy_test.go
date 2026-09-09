package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestPolicyChecksExistenceOfAbsoluteLiteralTildePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "~")
	if err := os.Mkdir(directory, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, ".env")
	g, err := New(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"path": path, "content": "new"})
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "write", Name: "write", Arguments: raw}
	result, err := g.Check(t.Context(), call)
	if err != nil || result.Decision != DecisionAllow {
		t.Fatalf("missing target = %+v, %v", result, err)
	}
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = g.Check(t.Context(), call)
	if err != nil || result.Decision != DecisionDeny || result.RuleID != "secret-files" {
		t.Fatalf("existing target = %+v, %v", result, err)
	}
}
