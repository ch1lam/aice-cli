package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestUsageReadDoesNotStartSessionOrConsumeTranscript(t *testing.T) {
	t.Parallel()
	s := &interactiveSession{model: llm.Model{ID: "test", ContextWindow: 1000}, transcript: &interaction.Transcript{}, sessionChanged: true}
	u, err := s.ReadUsage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s.conversation.store != nil || u.SessionID != "" || u.Context.Window != 1000 || !u.Context.Known || u.CostStatus != "Unavailable" || s.transcript == nil || !s.sessionChanged {
		t.Fatalf("unexpected read: %+v", u)
	}
}
func TestUsageCostCompletenessAcrossRecords(t *testing.T) {
	t.Parallel()
	h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{}, nil })
	s := h.session
	if err := s.ensureSessionStore(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, err := s.ReadUsage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if u.CostStatus != "Unavailable" {
		t.Fatal(u.CostStatus)
	}
	if err := appendSessionMessage(t.Context(), s.conversation.store, llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("hello").Part()}}); err != nil {
		t.Fatal(err)
	}
	for i, priced := range []bool{true, false} {
		usage := llm.Usage{InputTokens: 10, OutputTokens: 2}
		if priced {
			usage.Cost = &llm.Cost{Total: 0.02}
		}
		if err := appendSessionMessage(t.Context(), s.conversation.store, llm.AssistantMessage{Role: llm.RoleAssistant, API: "test-api", ModelID: s.model.ID, Provider: s.model.Provider, StopReason: llm.StopReasonStop, Usage: usage}); err != nil {
			t.Fatal(err)
		}
		u, err = s.ReadUsage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := "Estimate"
		if i > 0 {
			want = "Partial estimate"
		}
		if u.CostStatus != want || u.Usage.TotalCost != 0.02 {
			t.Fatalf("cost=%+v", u)
		}
	}
	snapshot, err := s.conversation.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	total := session.TotalUsage(snapshot)
	if u.Usage.InputTokens != total.InputTokens || u.Messages != 3 {
		t.Fatal("record scope changed")
	}
}
