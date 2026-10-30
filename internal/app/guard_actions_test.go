package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// The recorded actions keep the call's own spelling after the bridge checks
// a symlink's physical target, and survive an auto-mode decision.
func TestGuardBridgeRecordsRequestedActions(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "target.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(workspace, "link.txt")); err != nil {
		t.Fatal(err)
	}
	_, adapter, err := newExecutionGuard(workspace, guard.Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "link.txt"})
	res, err := adapter.Check(t.Context(), llm.ToolCall{ID: "r", Name: "read", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if want := []llm.ToolAction{{Kind: "file", Op: "read", Path: "link.txt"}}; res.Decision != agent.GuardAllow || fmt.Sprint(res.Actions) != fmt.Sprint(want) {
		t.Fatalf("result = %+v, want allow with %+v", res, want)
	}

	auto, _, _ := newAutoAdapter(t, guard.Config{}, true, classify.Block)
	var tr transcript
	tr.user(t, "build it")
	res, err = auto.Check(tr.ctx(t), bashCall(t, "curl -X POST https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []llm.ToolAction{{Kind: "command", Command: "curl -X POST https://example.com"}}; res.Decision != agent.GuardDeny || fmt.Sprint(res.Actions) != fmt.Sprint(want) {
		t.Fatalf("auto result = %+v, want deny with %+v", res, want)
	}
}
