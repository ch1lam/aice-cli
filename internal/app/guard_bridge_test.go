package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestGuardReadUnicodeTarget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		disk  string
	}{
		{name: "ideographic space", input: "@a\u3000b.txt", disk: "a b.txt"},
		{name: "medium mathematical space", input: "a\u205fb.txt", disk: "a b.txt"},
		{name: "em space", input: "a\u2003b.txt", disk: "a b.txt"},
		{name: "recursive NFD and curly", input: "d'ậ.txt", disk: "d’a\u0323\u0302.txt"},
		{name: "Greek NFD", input: "ά.txt", disk: "α\u0301.txt"},
		{name: "combining order", input: "a\u0301\u0323.txt", disk: "a\u0323\u0301.txt"},
		{name: "screenshot", input: "1 AM.txt", disk: "1\u202fAM.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			workspace, err := tool.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := tool.NewRead(workspace)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outside, "target.txt")
			if err := os.WriteFile(target, []byte("authorized content"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, test.disk)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			physical, err := filepath.EvalSymlinks(target)
			if err != nil {
				t.Fatal(err)
			}
			inner, gate, err := newExecutionGuard(root, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(tool.ReadRequest{Path: test.input})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "unicode", Name: "read", Arguments: raw}
			decision, err := gate.Check(t.Context(), call)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Decision != agent.GuardAsk || len(decision.Approvals) != 1 || decision.Approvals[0].Action.Path != physical {
				t.Fatalf("guard did not ask for physical target %q: %+v", physical, decision)
			}
			resolved, err := reader.ResolvePath(test.input)
			if err != nil || resolved != physical {
				t.Fatalf("read target = %q, %v; guard target = %q", resolved, err, physical)
			}
			inner.AllowPathSession(physical, false)
			decision, err = gate.Check(t.Context(), call)
			if err != nil || decision.Decision != agent.GuardAllow {
				t.Fatalf("authorized target rejected: %+v, %v", decision, err)
			}
			result, err := reader.Execute(t.Context(), call)
			if err != nil || len(result.Content) != 1 || result.Content[0].Text != "authorized content" {
				t.Fatalf("read = %+v, %v", result, err)
			}

			// The same tolerant alias must never bypass a hard policy denial,
			// including with yolo and an existing path-access grant.
			secret := filepath.Join(outside, ".env")
			if err := os.Rename(target, secret); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, test.disk)
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, link); err != nil {
				t.Fatal(err)
			}
			gate.yolo = true
			decision, err = gate.Check(t.Context(), call)
			if err != nil || decision.Decision != agent.GuardDeny || decision.RuleID != "secret-files" {
				t.Fatalf("protected target not denied: %+v, %v", decision, err)
			}
		})
	}
}

func TestGuardReadChecksWinningCandidate(t *testing.T) {
	t.Parallel()
	for _, protectBase := range []bool{false, true} {
		name := "protected fallback loses"
		if protectBase {
			name = "protected base wins"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			secret := filepath.Join(root, ".env")
			if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
			base, fallback := "it's.txt", "it’s.txt"
			plain, protected := base, fallback
			if protectBase {
				plain, protected = fallback, base
			}
			if err := os.WriteFile(filepath.Join(root, plain), []byte("plain"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, filepath.Join(root, protected)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			_, gate, err := newExecutionGuard(root, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(tool.ReadRequest{Path: base})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "conflict", Name: "read", Arguments: raw}
			decision, err := gate.Check(t.Context(), call)
			if err != nil {
				t.Fatal(err)
			}
			want := agent.GuardAllow
			if protectBase {
				want = agent.GuardDeny
			}
			if decision.Decision != want {
				t.Fatalf("guard = %+v, want %s", decision, want)
			}
			if !protectBase {
				workspace, err := tool.NewWorkspace(root)
				if err != nil {
					t.Fatal(err)
				}
				reader, err := tool.NewRead(workspace)
				if err != nil {
					t.Fatal(err)
				}
				result, err := reader.Execute(t.Context(), call)
				if err != nil || len(result.Content) != 1 || result.Content[0].Text != "plain" {
					t.Fatalf("read = %+v, %v", result, err)
				}
			}
		})
	}
}
