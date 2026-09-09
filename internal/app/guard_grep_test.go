package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func grepPathCall(t *testing.T, path string) llm.ToolCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"path": path, "pattern": "needle"})
	if err != nil {
		t.Fatal(err)
	}
	return llm.ToolCall{ID: "grep-path", Name: "grep", Arguments: raw}
}

func TestGuardGrepChecksNormalizedAndPhysicalTargets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"outside file", "outside directory", "protected target", "protected normalized alias", "protected original alias", "space normalization", "parent traversal", "read-only root"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			input, alias := "@alias\u3000file.txt", "alias file.txt"
			target := filepath.Join(outside, "target.txt")
			linkTarget := target
			want := agent.GuardAsk
			var readOnlyRoots []string
			switch kind {
			case "outside directory":
				input, alias, linkTarget = "@dir", "dir", outside
			case "protected target":
				target = filepath.Join(outside, ".env")
				linkTarget = target
				want = agent.GuardDeny
			case "protected normalized alias":
				input, alias = "@.env", ".env"
				target = filepath.Join(root, "plain.txt")
				linkTarget = target
				want = agent.GuardDeny
			case "protected original alias":
				input, alias = ".env", ".env"
				target = filepath.Join(root, "plain.txt")
				linkTarget = target
				want = agent.GuardDeny
			case "space normalization":
				target = filepath.Join(outside, "a b.txt")
				linkTarget = target
				input = filepath.Join(outside, "a\u3000b.txt")
			case "parent traversal":
				child := filepath.Join(outside, "child")
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				input, alias, linkTarget = "@alias/../target.txt", "alias", child
			case "read-only root":
				physicalRoot, err := filepath.EvalSymlinks(outside)
				if err != nil {
					t.Fatal(err)
				}
				readOnlyRoots = []string{physicalRoot}
				want = agent.GuardAllow
			}
			if err := os.WriteFile(target, []byte("needle actual target\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(linkTarget, filepath.Join(root, alias)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			inner, gate, err := newExecutionGuard(root, readOnlyRoots, false)
			if err != nil {
				t.Fatal(err)
			}
			call := grepPathCall(t, input)
			result, err := gate.Check(t.Context(), call)
			if err != nil || result.Decision != want {
				t.Fatalf("decision=%+v err=%v want=%s", result, err, want)
			}
			if want == agent.GuardDeny {
				gate.yolo = true
				inner.AllowPathSession(target, false)
				result, err = gate.Check(t.Context(), call)
				if err != nil || result.Decision != agent.GuardDeny {
					t.Fatalf("deny bypassed: %+v %v", result, err)
				}
				return
			}
			workspace, err := tool.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			_, physical, err := workspace.ResolveGrepPaths(input)
			if err != nil {
				t.Fatal(err)
			}
			if want == agent.GuardAsk {
				found := false
				for _, approval := range result.Approvals {
					if inner.ResolveAbsolute(approval.Action.Path, "grep") == physical {
						found = true
					}
				}
				if !found {
					t.Fatalf("actual target %q not checked: %+v", physical, result)
				}
				for _, approval := range result.Approvals {
					inner.AllowPathSession(inner.ResolveAbsolute(approval.Action.Path, "grep"), false)
				}
				result, err = gate.Check(t.Context(), call)
				if err != nil || result.Decision != agent.GuardAllow {
					t.Fatalf("grant rejected: %+v %v", result, err)
				}
			}
			if result.Revalidate == nil {
				t.Fatal("missing target revalidation")
			}
			if err := result.Revalidate(t.Context()); err != nil {
				t.Fatal(err)
			}
			grep, err := tool.NewGrep(workspace)
			if err != nil {
				t.Fatal(err)
			}
			output, err := grep.Execute(t.Context(), call)
			if err != nil || len(output.Content) == 0 || !strings.Contains(output.Content[0].Text, "needle actual target") {
				t.Fatalf("execution differs from checked target: %+v %v", output, err)
			}
		})
	}
}

func TestGuardGrepBlocksOutsideAndRetargetedPaths(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	first, second := filepath.Join(outside, "first"), filepath.Join(outside, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("needle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	workspace, err := tool.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	mode := guard.PathAccessBlock
	inner, err := guard.New(workspace.PhysicalPath(), guard.Config{PathAccess: guard.PathAccessConfig{Mode: &mode}})
	if err != nil {
		t.Fatal(err)
	}
	gate := &guardAdapter{inner: inner, yolo: true}
	call := grepPathCall(t, "@alias")
	result, err := gate.Check(t.Context(), call)
	if err != nil || result.Decision != agent.GuardDeny || result.RuleID != "pathAccess.block" {
		t.Fatalf("outside block bypassed: %+v %v", result, err)
	}
	_, gate, err = newExecutionGuard(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err = gate.Check(t.Context(), call)
	if err != nil || result.Revalidate == nil {
		t.Fatalf("missing revalidation: %+v %v", result, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := result.Revalidate(t.Context()); err == nil {
		t.Fatal("changed symlink accepted")
	}
}

func TestGuardGrepDefaultPathAndInvalidTargets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, gate, err := newExecutionGuard(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "grep", Name: "grep", Arguments: json.RawMessage(`{"pattern":"needle"}`)}
	result, err := gate.Check(t.Context(), call)
	if err != nil || result.Decision != agent.GuardAllow || result.Revalidate == nil {
		t.Fatalf("default path: %+v %v", result, err)
	}
	for _, raw := range []string{`null`, `{"path":42}`, `{"path":"missing"}`, `{"path":"@"}`} {
		call.Arguments = json.RawMessage(raw)
		if _, err := gate.Check(t.Context(), call); err == nil {
			t.Fatalf("invalid target accepted: %s", raw)
		}
	}
}
