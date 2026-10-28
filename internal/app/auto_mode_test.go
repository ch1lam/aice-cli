package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// scriptedClassifier returns decisions in order and records requests.
type scriptedClassifier struct {
	mu        sync.Mutex
	decisions []classify.Decision
	requests  []classify.Request
}

func (c *scriptedClassifier) classify(_ context.Context, req classify.Request) (classify.Verdict, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	decision := c.decisions[min(len(c.requests)-1, len(c.decisions)-1)]
	return classify.Verdict{Decision: decision, Reason: "because " + string(decision), Stage: 2, Classifier: "fake@v"}, nil
}

func newAutoAdapter(t *testing.T, cfg guard.Config, headless bool, decisions ...classify.Decision) (*guardAdapter, *scriptedClassifier, string) {
	t.Helper()
	workspace := t.TempDir()
	_, adapter, err := newExecutionGuard(workspace, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	classifier := &scriptedClassifier{decisions: decisions}
	adapter.auto = newAutoMode(classifier.classify, adapter.inner.Workspace(), config.AutoModeSettings{Block: []string{"never touch prod"}}, headless)
	return adapter, classifier, adapter.inner.Workspace()
}

func bashCall(t *testing.T, command string) llm.ToolCall {
	return llm.ToolCall{ID: "c", Name: "bash", Arguments: mustCommandArgs(t, command)}
}

func TestAutoModeRouting(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(t.TempDir(), "settings.json")
	tests := []struct {
		name       string
		call       func(t *testing.T, workspace string) llm.ToolCall
		want       agent.GuardDecision
		rule       string
		classified bool
	}{
		{"read-only bash runs", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "git status && rg TODO") }, agent.GuardAllow, "", false},
		{"workspace edit runs", func(t *testing.T, _ string) llm.ToolCall {
			return llm.ToolCall{Name: "write", Arguments: json.RawMessage(`{"path":"a.go","content":"x"}`)}
		}, agent.GuardAllow, "", false},
		{"read outside asks the classifier", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "cat /etc/hosts") }, agent.GuardAllow, "", true},
		{"ordinary bash asks the classifier", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "python3 -c 'print(1)'") }, agent.GuardAllow, "", true},
		{"outbound asks the classifier", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "git push origin dev") }, agent.GuardAllow, "", true},
		{"web fetch asks the classifier", func(t *testing.T, _ string) llm.ToolCall {
			return llm.ToolCall{Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com/"}`)}
		}, agent.GuardAllow, "", true},
		{"git hooks write asks the classifier", func(t *testing.T, _ string) llm.ToolCall {
			return llm.ToolCall{Name: "write", Arguments: json.RawMessage(`{"path":".git/hooks/pre-commit","content":"x"}`)}
		}, agent.GuardAllow, "", true},
		{"privileged stays with a person", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "sudo ls") }, agent.GuardAsk, guard.RuleBashPrivileged, false},
		{"control file stays with a person", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "aice mcp approve x") }, agent.GuardAsk, guard.RuleControlFile, false},
		{"credentials stay denied", func(t *testing.T, _ string) llm.ToolCall { return bashCall(t, "cat ~/.ssh/id_rsa") }, agent.GuardDeny, "credentials", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, classifier, workspace := newAutoAdapter(t, guard.Config{ControlFiles: []string{settings}}, false, classify.Allow)
			result, err := adapter.Check(t.Context(), test.call(t, workspace))
			if err != nil {
				t.Fatal(err)
			}
			rule := result.RuleID
			if len(result.Approvals) > 0 {
				rule = result.Approvals[0].RuleID
			}
			if result.Decision != test.want || rule != test.rule || (len(classifier.requests) > 0) != test.classified {
				t.Fatalf("result %+v rule %q classified=%d", result, rule, len(classifier.requests))
			}
			if test.classified && (result.Review == nil || result.Review.Decision != "allow" || result.Review.Classifier != "fake@v") {
				t.Fatalf("missing review: %+v", result.Review)
			}
		})
	}
}

func TestAutoModeBlocksAndFuses(t *testing.T) {
	t.Parallel()
	for _, headless := range []bool{false, true} {
		adapter, _, _ := newAutoAdapter(t, guard.Config{}, headless, classify.Block)
		adapter.auto.recordIntent("refactor the parser", "prompt")
		var decisions []agent.GuardDecision
		for i := range autoBlockStreak + 1 {
			result, err := adapter.Check(t.Context(), bashCall(t, "python3 step"+string(rune('a'+i))+".py"))
			if err != nil {
				t.Fatal(err)
			}
			decisions = append(decisions, result.Decision)
			if result.Decision == agent.GuardDeny && (result.RuleID != ruleAutoBlock || result.Review == nil || !strings.Contains(result.Reason, "safer approach")) {
				t.Fatalf("block result = %+v", result)
			}
		}
		want := []agent.GuardDecision{agent.GuardDeny, agent.GuardDeny, agent.GuardAsk, agent.GuardDeny}
		if headless {
			want = []agent.GuardDecision{agent.GuardDeny, agent.GuardDeny, agent.GuardDeny, agent.GuardDeny}
		}
		for i := range want {
			if decisions[i] != want[i] {
				t.Fatalf("headless=%v decisions = %v, want %v", headless, decisions, want)
			}
		}
	}
}

func TestAutoModeUnavailableAsksWithReason(t *testing.T) {
	t.Parallel()
	adapter, _, _ := newAutoAdapter(t, guard.Config{}, false, classify.Unavailable)
	result, err := adapter.Check(t.Context(), bashCall(t, "git push origin dev"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != agent.GuardAsk || result.Approvals[0].RuleID != guard.RuleBashOutbound || !strings.Contains(result.Approvals[0].Reason, "could not review") {
		t.Fatalf("unavailable = %+v", result)
	}
	result, err = adapter.Check(t.Context(), bashCall(t, "make build"))
	if err != nil || result.Decision != agent.GuardAsk || result.Approvals[0].RuleID != ruleAutoUnavailable || result.Approvals[0].Action.Command != "make build" {
		t.Fatalf("unavailable without gate approval = %+v, %v", result, err)
	}
	if !result.Valid() {
		t.Fatal("synthesized approval is invalid")
	}
}

func TestAutoModeRequestCacheAndHistory(t *testing.T) {
	t.Parallel()
	adapter, classifier, workspace := newAutoAdapter(t, guard.Config{}, false, classify.Allow)
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "config"), []byte("[core]\n\tbare = false\n[remote \"origin\"]\n\turl = git@github.com:acme/p.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter.auto.recordIntent("build the project", "prompt")
	for _, command := range []string{"git status", "make build", "make build"} {
		if _, err := adapter.Check(t.Context(), bashCall(t, command)); err != nil {
			t.Fatal(err)
		}
	}
	if len(classifier.requests) != 1 {
		t.Fatalf("classifier calls = %d, want 1 (read-only skipped, repeat cached)", len(classifier.requests))
	}
	req := classifier.requests[0]
	if len(req.Intent) != 1 || req.Intent[0].Text != "build the project" || len(req.History) != 1 || req.History[0].Outcome != "allowed" || !strings.Contains(string(req.History[0].Arguments), "git status") {
		t.Fatalf("request intent/history = %+v / %+v", req.Intent, req.History)
	}
	if req.Environment.GitBranch != "feature/x" || len(req.Environment.Remotes) != 1 || req.Environment.Slots.Block[0] != "never touch prod" {
		t.Fatalf("environment = %+v", req.Environment)
	}
	// A new user turn invalidates the cache; reset forgets the Session.
	adapter.auto.recordIntent("now run it again", "steer")
	if _, err := adapter.Check(t.Context(), bashCall(t, "make build")); err != nil || len(classifier.requests) != 2 {
		t.Fatalf("cache survived new intent: %d, %v", len(classifier.requests), err)
	}
	adapter.auto.reset()
	if _, err := adapter.Check(t.Context(), bashCall(t, "make build")); err != nil {
		t.Fatal(err)
	}
	if last := classifier.requests[len(classifier.requests)-1]; len(last.Intent) != 0 || len(last.History) != 0 {
		t.Fatalf("reset kept state: %+v", last)
	}
}

func TestAutoModeAttachmentsSkipClassifier(t *testing.T) {
	t.Parallel()
	adapter, classifier, _ := newAutoAdapter(t, guard.Config{}, false, classify.Allow)
	outside := filepath.Join(t.TempDir(), "notes.txt")
	result, err := adapter.checkInput(t.Context(), llm.ToolCall{Name: "read", Arguments: mustPathArgs(t, outside)})
	if err != nil || result.Decision != agent.GuardAsk || len(classifier.requests) != 0 {
		t.Fatalf("attachment = %+v, classified %d, %v", result, len(classifier.requests), err)
	}
}

func TestAutoRunLimits(t *testing.T) {
	t.Parallel()
	limits := autoRunLimits(config.Config{PermissionMode: config.PermissionModeAuto})
	if limits.GuardDenials != (agent.GuardDenialLimit{Consecutive: autoBlockStreak, Total: autoBlockTotal}) {
		t.Fatalf("auto limits = %+v", limits)
	}
	if limits := autoRunLimits(config.Config{PermissionMode: config.PermissionModeDefault}); limits.GuardDenials != (agent.GuardDenialLimit{}) {
		t.Fatalf("default limits = %+v", limits)
	}
}
