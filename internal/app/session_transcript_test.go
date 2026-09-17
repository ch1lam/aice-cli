package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestSessionTranscriptPreservesRecordedToolPresentation(t *testing.T) {
	t.Parallel()
	s := browserHarness(t)
	store := browserFixture(t, s, "tools", "", "", 100)
	call := llm.ToolCall{ID: "write-call", Name: "write", Arguments: []byte(`{"path":"result.txt","content":"original content"}`)}
	messages := []llm.AgentMessage{
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("Write it").Part()}, Timestamp: 100},
		llm.AssistantMessage{Role: llm.RoleAssistant, API: "api", Provider: "provider", ModelID: "model", Timestamp: 101,
			StopReason: llm.StopReasonToolUse, Content: []llm.ContentPart{llm.NewThinkingContent("a plan", "").Part(), {Type: llm.ContentTypeToolCall, ToolCall: &call}}},
		llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: call.ID, ToolName: call.Name, Timestamp: 102,
			Content:    []llm.ContentPart{llm.NewTextContent("recorded output").Part()},
			Diff:       llm.ToolDiff{Text: "+original content", Added: 1, StatsKnown: true},
			Truncation: llm.ToolTruncation{Reason: llm.TruncationByteLimit, OutputBytes: 100}},
	}
	if err := appendTestSessionMessages(t.Context(), store, messages); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := store.Snapshot()
	view, err := sessionTranscript(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 3 {
		t.Fatal(view.Entries)
	}
	got := view.Entries[2].Tool
	if got.Content != "original content" || !got.HasContent || got.Detail != "result.txt" || got.Output.Text != "recorded output" ||
		got.Diff.Added != 1 || got.Truncation.Reason == "" || view.Entries[1].Assistant.Thinking != "a plan" {
		t.Fatalf("lost recorded display data: %#v", view)
	}
}

func TestSessionResumeUsesTargetRoutingAndContext(t *testing.T) {
	t.Parallel()
	model := &routingModel{recordingModel: recordingModel{response: "continued answer"}}
	h := newSideHarness(t, func() (agent.Model, error) { return model, nil })
	workspace, err := tool.NewWorkspace(h.workspace)
	if err != nil {
		t.Fatal(err)
	}
	s := h.session
	s.workspace, s.workspacePath = workspace, workspace.PhysicalPath()
	target := browserFixture(t, s, "target", "Earlier request", "Earlier answer", 200)
	target.Close()
	settings := s.settingsSnapshot()
	if _, err := s.slashResume(t.Context(), interaction.CommandRequest{Arguments: "target"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.conversation.store.Close() })
	if err := runInteractive(t.Context(), s, "Continue", nil); err != nil {
		t.Fatal(err)
	}
	if len(model.ids) != 1 || model.ids[0] != "target" {
		t.Fatal(model.ids)
	}
	texts := strings.Join(requestMessageTexts(model.requests[0]), "|")
	if !strings.Contains(texts, "Earlier request|Earlier answer|Continue") {
		t.Fatal(texts)
	}
	if s.settingsSnapshot().model.ID != settings.model.ID || s.settingsSnapshot().options.Thinking != settings.options.Thinking {
		t.Fatal("resume changed model settings")
	}
}

func TestSessionSearchOtherBranchDoesNotCheckout(t *testing.T) {
	t.Parallel()
	s := browserHarness(t)
	store := browserFixture(t, s, "branches", "Original request", "Abandoned needle", 100)
	snapshot, _ := store.Snapshot()
	if _, err := checkoutSessionStore(t.Context(), store, snapshot.Messages[0].ID, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Snapshot()
	preview, err := s.PreviewSession(t.Context(), "branches", "needle")
	if err != nil || !strings.Contains(preview, "another branch") {
		t.Fatal(preview, err)
	}
	items, err := s.SearchSessions(t.Context(), "needle")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	after, _, err := session.Read(t.Context(), store.Path())
	if err != nil || after.LeafID != before.LeafID || len(after.LeafMoves) != len(before.LeafMoves) {
		t.Fatal("search moved branch", err)
	}
}
