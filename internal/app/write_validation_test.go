package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestApplicationPrintWriteContentValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   json.RawMessage
		wantError bool
	}{
		{name: "missing", wantError: true},
		{name: "null", content: json.RawMessage(`null`), wantError: true},
		{name: "wrong type", content: json.RawMessage(`42`), wantError: true},
		{name: "empty string", content: json.RawMessage(`""`)},
		{name: "normal content", content: json.RawMessage(`"updated"`)},
	}
	for _, test := range tests {
		for _, existing := range []bool{false, true} {
			target := "new"
			if existing {
				target = "existing"
			}
			t.Run(test.name+"/"+target, func(t *testing.T) {
				t.Parallel()
				workspace := t.TempDir()
				path := filepath.Join(workspace, "nested", "file.txt")
				if existing {
					if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				args := map[string]any{"path": "nested/file.txt"}
				if test.content != nil {
					args["content"] = test.content
				}
				payload, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				call := llm.ToolCall{ID: "write-1", Name: "write", Arguments: payload}
				_, gate, err := newExecutionGuard(workspace, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				decision, err := gate.Check(t.Context(), call)
				if err != nil || decision.Decision != agent.GuardAllow {
					t.Fatalf("Guard.Check() = %#v, %v, want allow", decision, err)
				}
				result := runPrintToolCall(t, workspace, call, false)
				if result.ToolCallID != call.ID || result.ToolName != call.Name || result.IsError != test.wantError {
					t.Fatalf("next model request tool result = %#v", result)
				}
				if test.wantError && !strings.Contains(toolResultText(t, result), "content") {
					t.Errorf("tool error lacks content diagnostic: %#v", result)
				}
				if test.wantError && !existing {
					if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
						t.Fatalf("invalid content created parent directory: %v", err)
					}
					return
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				want := "original"
				if !test.wantError {
					if err := json.Unmarshal(test.content, &want); err != nil {
						t.Fatal(err)
					}
				}
				if string(data) != want {
					t.Fatalf("file content = %q, want %q", data, want)
				}
			})
		}
	}
}

func TestApplicationPrintWriteGuardDenialPrecedesValidation(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".env")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "write-denied", Name: "write", Arguments: json.RawMessage(`{"path":".env","content":null}`)}
	_, gate, err := newExecutionGuard(workspace, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := gate.Check(t.Context(), call)
	if err != nil || decision.Decision != agent.GuardDeny {
		t.Fatalf("Guard.Check() = %#v, %v", decision, err)
	}
	result := runPrintToolCall(t, workspace, call, true)
	if !result.IsError || result.ToolCallID != call.ID || toolResultText(t, result) != decision.Reason {
		t.Fatalf("tool result = %#v, want paired guard denial %q", result, decision.Reason)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("denied write changed file: %q", data)
	}
}
