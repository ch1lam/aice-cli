package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// checkWrite runs the Guard and, for a permitted write, the pre-execution
// recheck that claims its files.
func checkWrite(t *testing.T, adapter *guardAdapter, ctx context.Context, name string, arguments map[string]any) agent.GuardResult {
	t.Helper()
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	res, err := adapter.Check(ctx, llm.ToolCall{ID: "call", Name: name, Arguments: data})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != agent.GuardDeny && res.Revalidate != nil {
		if err := res.Revalidate(ctx); err != nil {
			return agent.GuardResult{Decision: agent.GuardDeny, RuleID: ruleFileClaim, Reason: err.Error()}
		}
	}
	return res
}

func TestWriteClaimsKeepConcurrentWritersOffEachOthersFiles(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(workspace, "link.txt")); err != nil {
		t.Fatal(err)
	}
	_, adapter, err := newExecutionGuard(workspace, guard.Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	main := t.Context()
	child := withWriter(t.Context(), "bg-000001")
	write := func(path string) map[string]any { return map[string]any{"path": path, "content": "x"} }
	allowed := func(t *testing.T, res agent.GuardResult) {
		t.Helper()
		if res.Decision == agent.GuardDeny {
			t.Fatalf("denied: %s", res.Reason)
		}
	}
	claimed := func(t *testing.T, res agent.GuardResult) {
		t.Helper()
		if res.Decision != agent.GuardDeny || res.RuleID != ruleFileClaim {
			t.Fatalf("result = %+v, want a file claim denial", res)
		}
	}

	// Alone, the main agent claims nothing a later writer would trip on.
	allowed(t, checkWrite(t, adapter, main, "write", write("a.txt")))
	adapter.claims.begin("bg-000001")
	allowed(t, checkWrite(t, adapter, child, "write", write("a.txt")))
	claimed(t, checkWrite(t, adapter, main, "edit", map[string]any{"path": "a.txt", "old_string": "a", "new_string": "b"}))
	claimed(t, checkWrite(t, adapter, main, "write", write("link.txt")))
	claimed(t, checkWrite(t, adapter, main, "bash", map[string]any{"command": "echo x > a.txt", "description": "write"}))
	allowed(t, checkWrite(t, adapter, main, "write", write("b.txt")))
	claimed(t, checkWrite(t, adapter, child, "write", write("b.txt")))
	allowed(t, checkWrite(t, adapter, child, "bash", map[string]any{"command": "echo x > a.txt", "description": "own file"}))

	// A claim made after the Guard decided still wins at execution.
	data, _ := json.Marshal(write("c.txt"))
	pending, err := adapter.Check(main, llm.ToolCall{ID: "late", Name: "write", Arguments: data})
	if err != nil || pending.Decision == agent.GuardDeny {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	allowed(t, checkWrite(t, adapter, child, "write", write("c.txt")))
	if err := pending.Revalidate(main); err == nil {
		t.Fatal("a write claimed meanwhile was not refused at execution")
	}

	adapter.claims.end("bg-000001")
	allowed(t, checkWrite(t, adapter, main, "write", write("a.txt")))
	adapter.claims.begin("bg-000002")
	allowed(t, checkWrite(t, adapter, withWriter(t.Context(), "bg-000002"), "write", write("b.txt")))
}
