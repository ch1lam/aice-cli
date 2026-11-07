package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func summaryAskApproval() agent.GuardApproval {
	return agent.GuardApproval{
		RuleID: "bash.delete", Reason: "Command deletes files.", Pattern: "rm -rf build",
		Action: agent.GuardAction{Kind: "command", Command: "rm -rf build", ToolName: "bash"},
	}
}

// startSummaryAsk asks about a call in the background and returns the
// request the frontend received plus the handler's result channel.
func startSummaryAsk(t *testing.T, ctx context.Context, session *interactiveSession, call llm.ToolCall) (interaction.GuardRequest, <-chan agent.GuardAskReply) {
	t.Helper()
	replies := make(chan agent.GuardAskReply, 1)
	go func() {
		reply, _ := session.askToolCall(ctx, call, summaryAskApproval())
		replies <- reply
	}()
	select {
	case request := <-session.guardRequests:
		return request, replies
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt waited for the summary")
	}
	return interaction.GuardRequest{}, nil
}

func receiveSummary(t *testing.T, summaries <-chan interaction.GuardSummary) (interaction.GuardSummary, bool) {
	t.Helper()
	select {
	case summary, ok := <-summaries:
		return summary, ok
	case <-time.After(5 * time.Second):
		t.Fatal("summary channel was never closed")
	}
	return interaction.GuardSummary{}, false
}

func TestGuardSummaryArrivesAfterPrompt(t *testing.T) {
	t.Parallel()
	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	release := make(chan struct{})
	seen := make(chan classify.ExplainRequest, 1)
	session.explainer = guardExplainer{timeout: guardSummaryTimeout, explain: func(ctx context.Context, req classify.ExplainRequest) (classify.Explanation, error) {
		seen <- req
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > guardSummaryTimeout {
			t.Errorf("side call deadline = %v, %v; want within %v", deadline, ok, guardSummaryTimeout)
		}
		<-release
		return classify.Explanation{Intent: "清理构建产物", Action: "删除 build 目录", Risk: "目录中的文件会丢失"}, nil
	}}
	var tr transcript
	tr.user(t, "清理一下构建产物")
	call := bashCall(t, "rm -rf build")
	request, replies := startSummaryAsk(t, tr.ctx(t), session, call)
	if request.Summarize == nil {
		t.Fatal("a model call's prompt offers no summary")
	}
	select {
	case <-seen:
		t.Fatal("the side call started before a frontend showed the prompt")
	case <-time.After(20 * time.Millisecond):
	}
	summaries := request.Summarize()
	if again := request.Summarize(); again != summaries {
		t.Fatal("Summarize started a second side call")
	}
	select {
	case <-summaries:
		t.Fatal("summary arrived before the side call returned")
	default:
	}
	req := <-seen
	if len(req.Intent) != 1 || req.Intent[0].Text != "清理一下构建产物" || req.Action.Tool != "bash" || req.GateReason != "Command deletes files." {
		t.Fatalf("explain request = %+v", req)
	}
	close(release)
	summary, ok := receiveSummary(t, summaries)
	if !ok || summary != (interaction.GuardSummary{Intent: "清理构建产物", Action: "删除 build 目录", Risk: "目录中的文件会丢失"}) {
		t.Fatalf("summary = %+v, %v", summary, ok)
	}
	if _, ok := receiveSummary(t, summaries); ok {
		t.Fatal("summary channel delivered twice")
	}
	request.Reply <- interaction.GuardReply{OptionID: guardOptionAllowOnce}
	if reply := <-replies; reply.Decision != agent.GuardAllow {
		t.Fatalf("decision = %q, want allow: the summary must not change it", reply.Decision)
	}
}

func TestGuardSummaryFailureIsOmitted(t *testing.T) {
	t.Parallel()
	for name, explain := range map[string]explainFunc{
		"error": func(context.Context, classify.ExplainRequest) (classify.Explanation, error) {
			return classify.Explanation{}, errors.New("provider offline")
		},
		"timeout": func(ctx context.Context, _ classify.ExplainRequest) (classify.Explanation, error) {
			// The model answers only after the deadline: the late answer is dropped.
			<-ctx.Done()
			return classify.Explanation{Risk: "late"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := newGuardAskSession(t, t.TempDir(), guard.Config{})
			session.explainer = guardExplainer{explain: explain, timeout: 20 * time.Millisecond}
			request, replies := startSummaryAsk(t, t.Context(), session, bashCall(t, "rm -rf build"))
			if summary, ok := receiveSummary(t, request.Summarize()); ok {
				t.Fatalf("failed side call delivered %+v", summary)
			}
			request.Reply <- interaction.GuardReply{OptionID: guardOptionDeny}
			if reply := <-replies; reply.Decision != agent.GuardDeny {
				t.Fatalf("decision = %q, want deny", reply.Decision)
			}
		})
	}
}

func TestGuardSummaryCancelledWhenAnswered(t *testing.T) {
	t.Parallel()
	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	started, cancelled := make(chan struct{}), make(chan struct{})
	session.explainer = guardExplainer{timeout: time.Minute, explain: func(ctx context.Context, _ classify.ExplainRequest) (classify.Explanation, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return classify.Explanation{Risk: "too late"}, nil
	}}
	request, replies := startSummaryAsk(t, t.Context(), session, bashCall(t, "rm -rf build"))
	summaries := request.Summarize()
	<-started
	request.Reply <- interaction.GuardReply{OptionID: guardOptionAllowOnce}
	if reply := <-replies; reply.Decision != agent.GuardAllow {
		t.Fatalf("decision = %q, want allow", reply.Decision)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("answering the prompt did not cancel the side call")
	}
	if summary, ok := receiveSummary(t, summaries); ok {
		t.Fatalf("cancelled side call delivered %+v", summary)
	}
}

// A frontend that shows a prompt only after it ended starts no side call.
func TestGuardSummaryNotStartedAfterPromptEnded(t *testing.T) {
	t.Parallel()
	session := &interactiveSession{explainer: guardExplainer{timeout: time.Minute, explain: func(context.Context, classify.ExplainRequest) (classify.Explanation, error) {
		t.Error("an ended prompt started the explainer")
		return classify.Explanation{Risk: "x"}, nil
	}}}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if summary, ok := receiveSummary(t, session.summarizeGuardAsk(ended, bashCall(t, "ls"), summaryAskApproval())()); ok {
		t.Fatalf("ended prompt delivered %+v", summary)
	}
}

func TestGuardSummaryOnlyForModelCalls(t *testing.T) {
	t.Parallel()
	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	session.explainer = guardExplainer{timeout: time.Minute, explain: func(context.Context, classify.ExplainRequest) (classify.Explanation, error) {
		t.Error("an attachment prompt started the explainer")
		return classify.Explanation{}, nil
	}}
	_, request := handleGuardAskWithReply(t, session, llm.ToolCall{ID: "attach", Name: "read", Arguments: mustFilePathArgs(t, "/outside/a.txt")},
		pathAccessAskResult("/outside/a.txt"), interaction.GuardReply{OptionID: guardOptionDeny})
	if request.Summarize != nil {
		t.Fatal("an attachment prompt offers a summary")
	}

	// Without an explainer a model call's prompt has none either.
	session.explainer = guardExplainer{}
	go func() {
		request := <-session.guardRequests
		if request.Summarize != nil {
			t.Error("prompt without an explainer offers a summary")
		}
		request.Reply <- interaction.GuardReply{OptionID: guardOptionDeny}
	}()
	if _, err := session.askToolCall(t.Context(), bashCall(t, "rm -rf build"), summaryAskApproval()); err != nil {
		t.Fatal(err)
	}
}

// The explainer sees only typed text and the pending call: never tool
// output, assistant text, reasoning, attachments or earlier calls.
func TestExplainRequestExcludesAgentContent(t *testing.T) {
	t.Parallel()
	var tr transcript
	tr.user(t, "fix the build"+attachedFileMarker+"ATTACHED FILE BODY]")
	assistant := llm.AssistantMessage{Content: []llm.ContentPart{
		llm.NewThinkingContent("HIDDEN REASONING", "").Part(),
		llm.NewTextContent("ASSISTANT PROSE").Part(),
	}}
	tr.messages = append(tr.messages, assistant)
	tr.call(t, "earlier", "cat EARLIER_ARGUMENT", nil, false)
	tr.user(t, "then clean up")
	entries, err := transcriptFrom(tr.ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	call := bashCall(t, "rm -rf build")
	req := explainRequest(entries, call, summaryAskApproval())
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"TOOL OUTPUT", "ASSISTANT PROSE", "HIDDEN REASONING", "ATTACHED FILE BODY", "EARLIER_ARGUMENT"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("explain request contains %q: %s", leaked, encoded)
		}
	}
	if len(req.Intent) != 2 || req.Intent[0].Text != "fix the build" || req.Intent[1].Text != "then clean up" {
		t.Fatalf("intent = %+v", req.Intent)
	}
	if req.Action.Tool != "bash" || !strings.Contains(string(req.Action.Arguments), "rm -rf build") ||
		len(req.Action.Effects) != 1 || req.Action.Effects[0].Rule != "bash.delete" || req.Action.Effects[0].Target != "rm -rf build" {
		t.Fatalf("action = %+v", req.Action)
	}
}
