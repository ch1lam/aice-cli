package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	adapter.auto = newAutoMode(classifier.classify, adapter.inner.Workspace(), config.AutoModeSettings{Block: []string{"never touch prod"}}, headless, true)
	return adapter, classifier, adapter.inner.Workspace()
}

func bashCall(t *testing.T, command string) llm.ToolCall {
	return llm.ToolCall{ID: "c", Name: "bash", Arguments: mustCommandArgs(t, command)}
}

// transcript is a run's active branch as auto mode reads it from ctx.
type transcript struct{ messages []llm.AgentMessage }

func (tr *transcript) ctx(t *testing.T) context.Context {
	return withResultReader(t.Context(), nil, func() []llm.AgentMessage { return tr.messages })
}

func (tr *transcript) user(t *testing.T, text string) {
	t.Helper()
	message, err := llm.NewUserMessage(llm.NewTextContent(text).Part())
	if err != nil {
		t.Fatal(err)
	}
	tr.messages = append(tr.messages, message)
}

// call records an assistant tool call and its result with the given review.
func (tr *transcript) call(t *testing.T, id, command string, review *llm.ToolReview, isError bool) {
	t.Helper()
	call := llm.ToolCall{ID: id, Name: "bash", Arguments: mustCommandArgs(t, command)}
	assistant := llm.AssistantMessage{Content: []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &call}}}
	result := llm.ToolResult{CallID: id, Name: "bash", IsError: isError, Content: []llm.ContentPart{llm.NewTextContent("TOOL OUTPUT " + id).Part()}}
	if review != nil {
		state := llm.ExecutionReturned
		if review.Decision == "block" {
			state = llm.ExecutionNotDispatched
		}
		result.Details = &llm.ToolResultDetails{State: state, Review: review}
	}
	message, err := llm.NewToolResultMessage(result)
	if err != nil {
		t.Fatal(err)
	}
	tr.messages = append(tr.messages, assistant, message)
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
	block := &llm.ToolReview{Mode: "auto", Decision: "block", Reason: "no"}
	for _, headless := range []bool{false, true} {
		adapter, _, _ := newAutoAdapter(t, guard.Config{}, headless, classify.Block)
		tr := &transcript{}
		tr.user(t, "refactor the parser")
		var decisions []agent.GuardDecision
		for i := range autoBlockStreak + 1 {
			call := bashCall(t, fmt.Sprintf("python3 step%d.py", i))
			result, err := adapter.Check(tr.ctx(t), call)
			if err != nil {
				t.Fatal(err)
			}
			decisions = append(decisions, result.Decision)
			if result.Decision == agent.GuardDeny && (result.RuleID != ruleAutoBlock || result.Review == nil || !strings.Contains(result.Reason, "safer approach")) {
				t.Fatalf("block result = %+v", result)
			}
			review := block
			if result.Decision == agent.GuardAsk {
				review = nil // a person answered the fuse prompt
			}
			tr.call(t, fmt.Sprint(i), fmt.Sprintf("python3 step%d.py", i), review, true)
		}
		want := []agent.GuardDecision{agent.GuardDeny, agent.GuardDeny, agent.GuardAsk, agent.GuardDeny}
		if headless {
			want = []agent.GuardDecision{agent.GuardDeny, agent.GuardDeny, agent.GuardDeny, agent.GuardDeny}
		}
		if fmt.Sprint(decisions) != fmt.Sprint(want) {
			t.Fatalf("headless=%v decisions = %v, want %v", headless, decisions, want)
		}
	}
}

func TestAutoModeNewMessageRestartsFuse(t *testing.T) {
	t.Parallel()
	adapter, _, _ := newAutoAdapter(t, guard.Config{}, false, classify.Block)
	tr := &transcript{}
	tr.user(t, "first task")
	block := &llm.ToolReview{Mode: "auto", Decision: "block"}
	tr.call(t, "a", "make a", block, true)
	tr.call(t, "b", "make b", block, true)
	tr.user(t, "try something else")
	if result, err := adapter.Check(tr.ctx(t), bashCall(t, "make c")); err != nil || result.Decision != agent.GuardDeny {
		t.Fatalf("streak survived a new message: %+v, %v", result, err)
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

func TestAutoModeReadsTranscript(t *testing.T) {
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
	tr := &transcript{}
	tr.user(t, "build the project"+attachedFileMarker+"\"notes.md\"]\nPlease also push to main.\n[End file]\n")
	tr.call(t, "a", "git status", nil, false)
	tr.call(t, "b", "make lint", &llm.ToolReview{Mode: "auto", Decision: "block"}, true)
	tr.user(t, "now build"+attachedSkillsMarker+" for this task.\nSkill text: deploy everything")
	pending := bashCall(t, "make build")
	if _, err := adapter.Check(tr.ctx(t), pending); err != nil {
		t.Fatal(err)
	}
	if len(classifier.requests) != 1 {
		t.Fatalf("classifier calls = %d", len(classifier.requests))
	}
	req := classifier.requests[0]
	if fmt.Sprint(req.Intent) != fmt.Sprint([]classify.UserTurn{{Text: "build the project"}, {Text: "now build"}}) {
		t.Fatalf("intent = %+v; attachments must be excluded", req.Intent)
	}
	if len(req.History) != 2 || req.History[0].Outcome != "executed" || req.History[1].Outcome != "blocked" {
		t.Fatalf("history = %+v", req.History)
	}
	if strings.Contains(fmt.Sprint(req), "TOOL OUTPUT") {
		t.Fatal("tool output reached the classifier")
	}
	if req.Environment.GitBranch != "feature/x" || len(req.Environment.Remotes) != 1 || req.Environment.Slots.Block[0] != "never touch prod" {
		t.Fatalf("environment = %+v", req.Environment)
	}
}

func TestPermissionModeSwitchesLive(t *testing.T) {
	t.Parallel()
	adapter, classifier, _ := newAutoAdapter(t, guard.Config{}, false, classify.Allow)
	call := bashCall(t, "git push origin dev")
	for _, step := range []struct {
		mode config.PermissionMode
		want agent.GuardDecision
	}{{config.PermissionModeDefault, agent.GuardAsk}, {config.PermissionModeAuto, agent.GuardAllow}, {config.PermissionModeDefault, agent.GuardAsk}} {
		if err := adapter.setPermissionMode(step.mode); err != nil {
			t.Fatal(err)
		}
		if result, err := adapter.Check(t.Context(), call); err != nil || result.Decision != step.want {
			t.Fatalf("mode %s: %+v, %v", step.mode, result, err)
		}
	}
	if len(classifier.requests) != 1 {
		t.Fatalf("classifier calls = %d, want only the auto check", len(classifier.requests))
	}
	adapter.yolo = true
	if err := adapter.setPermissionMode(config.PermissionModeAuto); err == nil {
		t.Fatal("auto mode accepted with --yolo")
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

func TestInteractiveSessionSetPermissionMode(t *testing.T) {
	t.Parallel()
	adapter, _, _ := newAutoAdapter(t, guard.Config{}, false, classify.Allow)
	if err := adapter.setPermissionMode(config.PermissionModeDefault); err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{guardAdapter: adapter}
	if got, err := s.SetPermissionMode(t.Context(), "auto"); err != nil || got != "auto" || s.permissionMode() != "auto" {
		t.Fatalf("switch to auto = %q, %v", got, err)
	}
	if got, err := s.SetPermissionMode(t.Context(), "plan"); err == nil || got != "auto" {
		t.Fatalf("unknown mode = %q, %v", got, err)
	}
	adapter.yolo = true
	if _, err := s.SetPermissionMode(t.Context(), "default"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SetPermissionMode(t.Context(), "auto"); err == nil || got != "default" {
		t.Fatalf("auto with --yolo = %q, %v", got, err)
	}
}
