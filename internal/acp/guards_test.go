package acp

import (
	"context"
	"io"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// summaryClient answers a permission request only after it saw the
// summary update, or immediately when told not to wait.
type summaryClient struct {
	sdk.Client
	wait    bool
	updates chan sdk.SessionNotification
}

func (c *summaryClient) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	c.updates <- n
	return nil
}

func (c *summaryClient) RequestPermission(ctx context.Context, _ sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	if c.wait {
		select {
		case n := <-c.updates:
			c.updates <- n
		case <-ctx.Done():
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected("allow-once")}, nil
}

func newSummaryAgent(t *testing.T, client *summaryClient) *agent {
	t.Helper()
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	sdk.NewClientSideConnection(client, clientOut, clientIn)
	a := &agent{sessionID: "s", ready: make(chan struct{})}
	a.conn = sdk.NewAgentSideConnection(a, agentOut, agentIn)
	close(a.ready)
	return a
}

func TestGuardSummaryUpdatesPendingToolCall(t *testing.T) {
	client := &summaryClient{wait: true, updates: make(chan sdk.SessionNotification, 4)}
	a := newSummaryAgent(t, client)
	summaries := make(chan interaction.GuardSummary, 1)
	summaries <- interaction.GuardSummary{Intent: "清理构建产物", Action: "删除 build 目录", Risk: "文件会丢失"}
	close(summaries)
	reply := make(chan interaction.GuardReply, 1)
	a.answerGuard(t.Context(), interaction.GuardRequest{
		ID: "call-1", ToolName: "bash", Command: "rm -rf build",
		Options: []interaction.GuardOption{{ID: "allow-once"}, {ID: "deny", Deny: true}},
		Reply:   reply, Done: make(chan struct{}),
		Summarize: func() <-chan interaction.GuardSummary { return summaries },
	})
	if got := <-reply; got.OptionID != "allow-once" {
		t.Fatalf("reply = %q: the summary must not change the answer", got.OptionID)
	}
	n := <-client.updates
	update := n.Update.ToolCallUpdate
	if update == nil || update.ToolCallId != "call-1" || len(update.Content) != 1 || update.Content[0].Content == nil ||
		update.Content[0].Content.Content.Text == nil || update.Content[0].Content.Content.Text.Text != "Why: 清理构建产物\nDoes: 删除 build 目录\nRisk: 文件会丢失" {
		t.Fatalf("summary update = %+v", n.Update)
	}
}

func TestGuardSummaryAbsentSendsNoUpdate(t *testing.T) {
	client := &summaryClient{updates: make(chan sdk.SessionNotification, 4)}
	a := newSummaryAgent(t, client)
	summaries := make(chan interaction.GuardSummary)
	close(summaries)
	reply := make(chan interaction.GuardReply, 1)
	a.answerGuard(t.Context(), interaction.GuardRequest{
		ID: "call-1", Options: []interaction.GuardOption{{ID: "allow-once"}},
		Reply: reply, Done: make(chan struct{}),
		Summarize: func() <-chan interaction.GuardSummary { return summaries },
	})
	<-reply
	select {
	case n := <-client.updates:
		t.Fatalf("omitted summary sent %+v", n.Update)
	case <-time.After(50 * time.Millisecond):
	}
}

// Answering before the summary arrives returns without waiting for it.
func TestGuardSummaryDoesNotDelayAnswer(t *testing.T) {
	client := &summaryClient{updates: make(chan sdk.SessionNotification, 4)}
	a := newSummaryAgent(t, client)
	reply := make(chan interaction.GuardReply, 1)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		a.answerGuard(t.Context(), interaction.GuardRequest{
			ID: "call-1", Options: []interaction.GuardOption{{ID: "allow-once"}},
			Reply: reply, Done: make(chan struct{}),
			Summarize: func() <-chan interaction.GuardSummary { return make(chan interaction.GuardSummary) },
		})
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the answer waited for a summary that never came")
	}
	if got := <-reply; got.OptionID != "allow-once" {
		t.Fatalf("reply = %q", got.OptionID)
	}
}
