package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestMutationSessionPreservesOutcomeOnCancellation(t *testing.T) {
	for _, name := range []string{"write", "edit"} {
		for _, committed := range []bool{false, true} {
			boundary := "before-execution"
			if committed {
				boundary = "after-commit"
			}
			t.Run(name+"/"+boundary, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				ws, err := tool.NewWorkspace(root)
				if err != nil {
					t.Fatal(err)
				}
				var mutation agent.Tool
				call := llm.ToolCall{ID: "mutation-1", Name: name}
				wantText := ""
				if name == "write" {
					mutation, err = tool.NewWrite(ws)
					call.Arguments = []byte(`{"path":"target","content":"new"}`)
					wantText = "Wrote 3 bytes to target."
				} else {
					mutation, err = tool.NewEdit(ws)
					call.Arguments = []byte(`{"path":"target","edits":[{"oldText":"old","newText":"new"}]}`)
					wantText = "Successfully replaced 1 block(s) in target."
				}
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				wrapped := newAppTestTool(name, func(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
					if !committed {
						cancel()
					}
					result, err := mutation.Execute(ctx, call)
					cancel()
					return result, err
				})
				model := &toolLoopModel{firstCall: &call}
				sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
				store := createAppTestSession(t, sessionPath, root)
				t.Cleanup(func() {
					if err := store.Close(); err != nil {
						t.Error(err)
					}
				})
				runner := &interactiveSession{
					loop:         mustAppLoop(t, model, []agent.Tool{wrapped}),
					conversation: conversationState{store: store},
					model:        deepseek.DefaultModel(),
				}
				if err := runInteractive(ctx, runner, "change target", nil); !errors.Is(err, context.Canceled) {
					t.Fatalf("run error = %v", err)
				}
				if len(model.requests) != 1 {
					t.Fatalf("requests after cancellation = %d", len(model.requests))
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				history, err := sessionHistory(openSessionSnapshot(t, sessionPath))
				if err != nil {
					t.Fatal(err)
				}
				results := 0
				for _, message := range history {
					result, ok := message.(llm.ToolResultMessage)
					if !ok {
						continue
					}
					results++
					if result.ToolCallID != call.ID || result.IsError == committed || len(result.Content) != 1 {
						t.Fatalf("restored result = %+v", result)
					}
					if committed && result.Content[0].Text != wantText {
						t.Fatalf("lost success: %+v", result)
					}
					if !committed && !strings.Contains(result.Content[0].Text, context.Canceled.Error()) {
						t.Fatalf("lost cancellation: %+v", result)
					}
				}
				if results != 1 {
					t.Fatalf("restored results = %d, want exactly one", results)
				}
				want := "old"
				if committed {
					want = "new"
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != want {
					t.Fatalf("target = %q, error = %v", data, err)
				}
			})
		}
	}
}
