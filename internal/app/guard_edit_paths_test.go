package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestGuardEditPhysicalTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"outside", "outside parent", "protected target", "protected alias", "protected chain", "parent traversal", "literal tilde", "dangling", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "file.txt")
			input, destination := "alias", target
			want := agent.GuardAsk
			switch kind {
			case "protected target", "protected chain":
				target = filepath.Join(outside, ".env")
				destination = target
				want = agent.GuardDeny
			case "protected alias":
				input = ".env"
				want = agent.GuardDeny
			case "outside parent":
				destination = outside
				input = "alias/file.txt"
			case "parent traversal":
				child := filepath.Join(outside, "child")
				if err := os.Mkdir(child, 0750); err != nil {
					t.Fatal(err)
				}
				destination, input = child, "alias/../file.txt"
			case "literal tilde":
				if err := os.Mkdir(filepath.Join(root, "~"), 0750); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(root, "~", ".env")
				destination = target
				input = "~/.env"
				want = agent.GuardDeny
			case "dangling":
				destination = filepath.Join(outside, "missing")
			case "cycle":
				destination = "alias"
			}
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "protected chain" {
				if err := os.Symlink(target, filepath.Join(root, "middle")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				destination = "middle"
			}
			if kind != "literal tilde" {
				linkName := "alias"
				if kind == "protected alias" {
					linkName = ".env"
				}
				if err := os.Symlink(destination, filepath.Join(root, linkName)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			inner, gate, err := newExecutionGuard(root, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			args, err := json.Marshal(map[string]any{"path": input, "edits": []map[string]string{{"oldText": "old", "newText": "new"}}})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "edit", Name: "edit", Arguments: args}
			result, err := gate.Check(t.Context(), call)
			if kind == "dangling" || kind == "cycle" {
				if err == nil {
					t.Fatalf("unresolved path accepted: %+v", result)
				}
				return
			}
			if err != nil || result.Decision != want {
				t.Fatalf("check = %+v, %v; want %s", result, err, want)
			}
			if want == agent.GuardDeny {
				gate.yolo = true
				inner.AllowPathSession(target, false)
				result, err = gate.Check(t.Context(), call)
				if err != nil || result.Decision != agent.GuardDeny {
					t.Fatalf("yolo bypassed policy: %+v, %v", result, err)
				}
				return
			}
			workspace, err := tool.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			editor, err := tool.NewEdit(workspace)
			if err != nil {
				t.Fatal(err)
			}
			physical, err := editor.ResolvePath(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Approvals) != 1 || result.Approvals[0].Action.Path != physical {
				t.Fatalf("approval != edit target %q: %+v", physical, result)
			}
			inner.AllowPathSession(physical, false)
			result, err = gate.Check(t.Context(), call)
			if err != nil || result.Decision != agent.GuardAllow {
				t.Fatalf("grant rejected: %+v, %v", result, err)
			}
			if _, err := editor.Execute(t.Context(), call); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "new" {
				t.Fatalf("target = %q, %v", data, err)
			}
		})
	}
}

func TestGuardEditRejectsRetargetedLink(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	first, second := filepath.Join(outside, "first"), filepath.Join(outside, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, gate, err := newExecutionGuard(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "edit", Name: "edit", Arguments: json.RawMessage(`{"path":"alias","edits":[{"oldText":"old","newText":"new"}]}`)}
	result, err := gate.Check(t.Context(), call)
	if err != nil || result.Decision != agent.GuardAsk || result.Revalidate == nil {
		t.Fatalf("check = %+v, %v", result, err)
	}
	if err := result.Revalidate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := result.Revalidate(t.Context()); err == nil {
		t.Fatal("retargeted link accepted")
	}
	for _, path := range []string{first, second} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "old" {
			t.Fatalf("file changed: %q, %v", data, err)
		}
	}
}

func TestGuardEditPhysicalTargetHardPolicies(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"outside block", "read only"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "target.txt")
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			physicalRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			mode := guard.PathAccessBlock
			config := guard.Config{PathAccess: guard.PathAccessConfig{Mode: &mode}}
			wantRule := "pathAccess.block"
			if name == "read only" {
				mode = guard.PathAccessAllow
				config.Policies = []guard.PolicyRule{{ID: "read-only-target", Patterns: []guard.PatternConfig{{Pattern: "target.txt"}}, Protection: guard.ProtectionReadOnly}}
				wantRule = "read-only-target"
			}
			inner, err := guard.New(physicalRoot, config)
			if err != nil {
				t.Fatal(err)
			}
			gate := &guardAdapter{inner: inner, yolo: true}
			result, err := gate.Check(t.Context(), llm.ToolCall{ID: "edit", Name: "edit", Arguments: json.RawMessage(`{"path":"alias","edits":[{"oldText":"old","newText":"new"}]}`)})
			if err != nil || result.Decision != agent.GuardDeny || result.RuleID != wantRule {
				t.Fatalf("check = %+v, %v; want %s", result, err, wantRule)
			}
		})
	}
}

func TestGuardEditRevalidatesExistingTarget(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"removed", "directory", "parent retargeted"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			first, second := filepath.Join(outside, "first"), filepath.Join(outside, "second")
			for _, dir := range []string{first, second} {
				if err := os.Mkdir(dir, 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "file"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(root, "alias")
			if err := os.Symlink(first, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			_, gate, err := newExecutionGuard(root, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "edit", Name: "edit", Arguments: json.RawMessage(`{"path":"alias/file","edits":[{"oldText":"old","newText":"new"}]}`)}
			result, err := gate.Check(t.Context(), call)
			if err != nil || result.Decision != agent.GuardAsk || result.Revalidate == nil {
				t.Fatalf("check = %+v, %v", result, err)
			}
			if err := result.Revalidate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if change == "parent retargeted" {
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(second, link); err != nil {
					t.Fatal(err)
				}
			} else {
				path := filepath.Join(first, "file")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "directory" {
					if err := os.Mkdir(path, 0750); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := result.Revalidate(t.Context()); err == nil {
				t.Fatal("changed target accepted after approval")
			}
			data, err := os.ReadFile(filepath.Join(second, "file"))
			if err != nil || string(data) != "old" {
				t.Fatalf("other target changed: %q, %v", data, err)
			}
		})
	}
}
