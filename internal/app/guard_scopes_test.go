package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

// runGuardedFake uses the real policy gate, app mapper and UI-reply bridge, and
// Loop. The bash tool only increments a counter; no shell command runs.
func runGuardedFake(t *testing.T, ctx context.Context, session *interactiveSession, command string, yolo bool, choose func(int, agent.GuardApproval) string) (int, []interaction.GuardRequest) {
	t.Helper()
	call := llm.ToolCall{ID: "call-scopes", Name: "bash", Arguments: mustCommandArgs(t, command)}
	model := &toolLoopModel{firstCall: &call}
	executions := 0
	tool := newAppTestTool("bash", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		executions++
		return llm.ToolResult{Content: []llm.ContentPart{llm.NewTextContent("fake result").Part()}}, nil
	})
	var requests []interaction.GuardRequest
	options := []agent.LoopOption{agent.WithGuard(&guardAdapter{inner: session.guard, yolo: yolo})}
	if choose != nil {
		options = append(options, agent.WithGuardAskHandler(func(ctx context.Context, call llm.ToolCall, approval agent.GuardApproval) (agent.GuardAskReply, error) {
			if executions != 0 {
				t.Fatal("tool executed before every approval was resolved")
			}
			option := choose(len(requests), approval)
			if ctx.Err() != nil {
				return agent.GuardAskReply{Decision: agent.GuardAllow}, nil
			}
			reply, request := handleGuardAskWithReply(t, session, call, approval, interaction.GuardReply{OptionID: option})
			requests = append(requests, request)
			return reply, nil
		}))
	}
	loop, err := agent.NewLoop(model, []agent.Tool{tool}, options...)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent("test authorization").Part())
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(ctx, agent.RunInput{Model: deepseek.DefaultModel(), Prompt: prompt}, nil)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	results := 0
	for _, message := range result.Messages() {
		if toolResult, ok := message.(llm.ToolResultMessage); ok {
			results++
			if toolResult.ToolCallID != call.ID || toolResult.IsError != (executions == 0) {
				t.Fatalf("paired tool result = %#v, executions = %d", toolResult, executions)
			}
		}
	}
	if results != 1 {
		t.Fatalf("tool results = %d, want one", results)
	}
	return executions, requests
}

func TestGuardAllScopesMustBeApproved(t *testing.T) {
	for _, test := range []struct {
		name                   string
		options                []string
		wantCalls, wantPrompts int
	}{
		{"all once", []string{guardOptionAllowOnce, guardOptionAllowOnce, guardOptionAllowOnce}, 1, 3},
		{"second denied", []string{guardOptionAllowOnce, guardOptionDeny}, 0, 2},
		{"third denied", []string{guardOptionAllowOnce, guardOptionAllowOnce, guardOptionDeny}, 0, 3},
		{"invalid reply", []string{guardOptionAllowOnce, "not-offered"}, 0, 2},
		{"command grant is not path grant", []string{guardOptionAllowRunCommand, guardOptionAllowOnce, guardOptionDeny}, 0, 3},
		{"file grant is not sibling grant", []string{guardOptionAllowOnce, guardOptionAllowRunFile, guardOptionDeny}, 0, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := newGuardAskSession(t, t.TempDir(), guard.Config{})
			outside := filepath.ToSlash(t.TempDir())
			command := fmt.Sprintf("rm -rf %q %q", outside+"/a.txt", outside+"/b.txt")
			calls, prompts := runGuardedFake(t, t.Context(), session, command, false, func(i int, _ agent.GuardApproval) string {
				if i >= len(test.options) {
					t.Fatalf("unexpected approval %d", i)
				}
				return test.options[i]
			})
			if calls != test.wantCalls || len(prompts) != test.wantPrompts {
				t.Fatalf("calls=%d prompts=%d", calls, len(prompts))
			}
			remaining, err := session.guard.Check(t.Context(), llm.ToolCall{Name: "bash", Arguments: mustCommandArgs(t, command)})
			if err != nil {
				t.Fatal(err)
			}
			wantRemaining := 3
			if slices.Contains(test.options, guardOptionAllowRunCommand) || slices.Contains(test.options, guardOptionAllowRunFile) {
				wantRemaining = 2
			}
			if remaining.Decision != guard.DecisionAsk || len(remaining.Approvals) != wantRemaining {
				t.Fatalf("remaining approvals: %#v", remaining)
			}
			for i, prompt := range prompts {
				if i == 0 && prompt.RuleID != guardRuleDangerous {
					t.Fatalf("first scope = %s", prompt.RuleID)
				}
				if i > 0 && (prompt.RuleID != guardRulePathAccessAsk || prompt.Path == "") {
					t.Fatalf("path scope = %#v", prompt)
				}
			}
			// Once grants do not leak to the next invocation, even after a later denial.
			if test.options[0] == guardOptionAllowOnce {
				_, again := runGuardedFake(t, t.Context(), session, command, false, func(_ int, _ agent.GuardApproval) string { return guardOptionDeny })
				if len(again) != 1 || again[0].RuleID != guardRuleDangerous {
					t.Fatalf("once leaked: %#v", again)
				}
			}
		})
	}
}

func TestGuardDenyWinsBeforeAnyApproval(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		for _, command := range []string{"sudo cat .env", "cat ../outside.txt .env", "cat .env ../outside.txt", "sudo cat ../outside.txt .env"} {
			t.Run(fmt.Sprintf("yolo=%v/%s", yolo, command), func(t *testing.T) {
				gate, err := guard.NewWithExists(t.TempDir(), guard.Config{}, func(string, string) bool { return true })
				if err != nil {
					t.Fatal(err)
				}
				session := &interactiveSession{guard: gate, guardRequests: make(chan interaction.GuardRequest, 1)}
				calls, prompts := runGuardedFake(t, t.Context(), session, command, yolo, func(_ int, _ agent.GuardApproval) string {
					t.Fatal("hard denial reached UI")
					return guardOptionAllowOnce
				})
				if calls != 0 || len(prompts) != 0 {
					t.Fatal("hard denial bypassed")
				}
			})
		}
	}
}

func TestGuardMultipleScopesYoloAndNoninteractive(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		session := newGuardAskSession(t, t.TempDir(), guard.Config{})
		calls, prompts := runGuardedFake(t, t.Context(), session, "rm -rf ../a.txt ../b.txt", yolo, nil)
		if (calls == 1) != yolo || len(prompts) != 0 {
			t.Fatalf("yolo=%v calls=%d prompts=%d", yolo, calls, len(prompts))
		}
	}
}

func TestGuardCancellationBeforeExecution(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		t.Run(fmt.Sprint(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			session := newGuardAskSession(t, t.TempDir(), guard.Config{})
			calls, _ := runGuardedFake(t, ctx, session, "rm -rf ../a.txt ../b.txt", false, func(i int, _ agent.GuardApproval) string {
				if i == cancelAt {
					cancel()
				}
				return guardOptionAllowOnce
			})
			if calls != 0 {
				t.Fatal("cancelled tool executed")
			}
		})
	}
}

func TestGuardNormalizedPathApprovalDeduplication(t *testing.T) {
	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	calls, prompts := runGuardedFake(t, t.Context(), session, "cat ../a.txt .././a.txt ../b.txt", false, func(_ int, _ agent.GuardApproval) string { return guardOptionAllowOnce })
	if calls != 1 || len(prompts) != 2 {
		t.Fatalf("calls=%d prompts=%d", calls, len(prompts))
	}
	paths := []string{prompts[0].Path, prompts[1].Path}
	if !slices.Equal(paths, []string{"../a.txt", "../b.txt"}) {
		t.Fatalf("paths=%v", paths)
	}
}
