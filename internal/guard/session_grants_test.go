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
	configuredPath := filepath.Join(t.TempDir(), "configured.txt")
	g, err := NewWithExists(t.TempDir(), Config{
		ReadOnlyRoots: []string{readRoot},
		PathAccess:    PathAccessConfig{AllowedPaths: []AllowedPath{{Kind: "file", Path: configuredPath}}},
		PermissionGate: PermissionGateConfig{
			AllowedPatterns:  []PatternConfig{{Pattern: "rm -rf ./configured"}},
			AutoDenyPatterns: []PatternConfig{{Pattern: "never-allow"}},
		},
	}, alwaysExists)
	if err != nil {
		t.Fatal(err)
	}
	grants := []struct {
		name    string
		allow   func()
		call    llm.ToolCall
		without Decision
	}{
		{"file policy", func() { g.AllowSession(".env") }, toolCall("read", map[string]any{"path": ".env"}), DecisionDeny},
		{"file path", func() { g.AllowPathSession(outsideFile, false) }, toolCall("read", map[string]any{"path": outsideFile}), DecisionAsk},
		{"directory path", func() { g.AllowPathSession(outsideDir, true) }, toolCall("read", map[string]any{"path": filepath.Join(outsideDir, "child.txt")}), DecisionAsk},
		{"exact command", func() { g.AllowCommandSession("rm -rf ./scratch") }, toolCall("bash", map[string]any{"command": "rm -rf ./scratch"}), DecisionAsk},
		{"command prefix", func() { g.AllowCommandPrefixSession("sudo") }, toolCall("bash", map[string]any{"command": "sudo echo hi"}), DecisionAsk},
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
		check(t, toolCall("read", map[string]any{"path": configuredPath}), DecisionAllow)
		check(t, toolCall("read", map[string]any{"path": filepath.Join(readRoot, "guide.md")}), DecisionAllow)
		check(t, toolCall("write", map[string]any{"path": filepath.Join(readRoot, "guide.md")}), DecisionAsk)
		check(t, toolCall("bash", map[string]any{"command": "rm -rf ./configured-other"}), DecisionAllow)
		check(t, toolCall("bash", map[string]any{"command": "echo never-allow"}), DecisionDeny)
		check(t, toolCall("read", map[string]any{"path": "README.md"}), DecisionAllow)
	}
}
