package guard

import (
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestResetSessionGrants(t *testing.T) {
	t.Parallel()

	outsideFile := filepath.Join(t.TempDir(), "file.txt")
	outsideDir := t.TempDir()
	readRoot := t.TempDir()
	workspace := t.TempDir()
	g := newTestGuard(t, workspace, readRoot)
	grants := []struct {
		name    string
		allow   func()
		call    llm.ToolCall
		without Decision
	}{
		{"file path", func() { g.AllowPathSession(outsideFile, false, false) }, toolCall("read", map[string]any{"path": outsideFile}), DecisionAsk},
		{"directory path", func() { g.AllowPathSession(outsideDir, true, false) }, toolCall("read", map[string]any{"path": filepath.Join(outsideDir, "child.txt")}), DecisionAsk},
		{"directory write", func() { g.AllowPathSession(outsideDir, true, true) }, toolCall("write", map[string]any{"path": filepath.Join(outsideDir, "child.txt")}), DecisionAsk},
		{"exact command", func() { g.AllowCommandSession("git push") }, toolCall("bash", map[string]any{"command": "git push"}), DecisionAsk},
		{"unknown tool", func() { g.AllowToolSession("custom") }, toolCall("custom", nil), DecisionAsk},
	}
	check := func(t *testing.T, call llm.ToolCall, want Decision) {
		t.Helper()
		result, err := g.Check(t.Context(), call)
		if err != nil || result.Decision != want {
			t.Fatalf("Check(%s) = %#v, error = %v, want %s", call.Arguments, result, err, want)
		}
	}
	for range 2 {
		for _, grant := range grants {
			grant.allow()
			check(t, grant.call, DecisionAllow)
		}
		g.ResetSessionGrants()
		for _, grant := range grants {
			t.Run(grant.name, func(t *testing.T) { check(t, grant.call, grant.without) })
		}
		check(t, toolCall("read", map[string]any{"path": filepath.Join(readRoot, "guide.md")}), DecisionAllow)
		check(t, toolCall("write", map[string]any{"path": filepath.Join(readRoot, "guide.md")}), DecisionAsk)
		check(t, toolCall("read", map[string]any{"path": "README.md"}), DecisionAllow)
	}
}

// A rebased Guard applies the same policy to another workspace: relative
// rules name its files, extra read-only roots are readable, and Session
// grants stay with the original.
func TestRebaseKeepsPolicyButNotSessionGrants(t *testing.T) {
	t.Parallel()
	workspace, other, readRoot, outside := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	g, err := New(workspace, Config{Rules: Rules{Deny: []string{"write(secret/**)"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.AllowPathSession(outside, true, true)
	if err := g.AddRule(RuleDecisionDeny, "bash(make deploy)"); err != nil {
		t.Fatal(err)
	}
	rebased, err := g.Rebase(other, readRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		call llm.ToolCall
		want Decision
	}{
		{toolCall("write", map[string]any{"path": "notes.txt"}), DecisionAllow},
		{toolCall("write", map[string]any{"path": filepath.Join(other, "secret", "k")}), DecisionDeny},
		{toolCall("write", map[string]any{"path": filepath.Join(workspace, "secret", "k")}), DecisionAsk},
		{toolCall("bash", map[string]any{"command": "make deploy"}), DecisionDeny},
		{toolCall("read", map[string]any{"path": filepath.Join(readRoot, "x")}), DecisionAllow},
		{toolCall("write", map[string]any{"path": filepath.Join(outside, "x")}), DecisionAsk},
	} {
		result, err := rebased.Check(t.Context(), test.call)
		if err != nil || result.Decision != test.want {
			t.Errorf("Check(%s %s) = %s, %v; want %s", test.call.Name, test.call.Arguments, result.Decision, err, test.want)
		}
	}
	if rebased.Workspace() != other {
		t.Fatalf("workspace = %q", rebased.Workspace())
	}
}
