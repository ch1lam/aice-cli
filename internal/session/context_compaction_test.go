package session_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestContextCompactionSharesSessionCutAndProtectsUnansweredUsers(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	initial := namedTextMessages("inspect", strings.Repeat("x", 12000), 0)
	pending := llm.UserMessage{Role: llm.RoleUser, Timestamp: 77, Content: []llm.ContentPart{llm.NewTextContent("preserve exact requirement").Part(), llm.NewTextContent("including separate blocks").Part()}}
	messages := append(initial, pending, pending)
	appendMessages(t, store, "source", messages...)
	settings := session.CompactionSettings{KeepRecentTokens: 20000, MaxRetainedTokens: 1000}
	pure, err := session.PrepareContextCompaction(messages, settings)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := session.PrepareCompaction(snapshotOf(t, store), settings)
	if err != nil {
		t.Fatal(err)
	}
	if pure.FirstKeptIndex != 2 || !reflect.DeepEqual(pure.RetainedMessages, []llm.AgentMessage{pending, pending}) {
		t.Fatalf("cut changed unanswered users: %#v", pure)
	}
	if !reflect.DeepEqual(persisted.MessagesToSummarize, pure.MessagesToSummarize) || persisted.RetainedMessageCount != len(pure.RetainedMessages) || persisted.TokensBefore != pure.TokensBefore {
		t.Fatal("memory and Session cuts differ")
	}
	summary, err := session.NewContextSummary("completed inspection", pure.TokensBefore, 1, pure.RetainedMessages)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Timestamp != 78 {
		t.Fatalf("summary timestamp=%d, want after retained context", summary.Timestamp)
	}
}

func TestContextCompactionPreservesOversizedInputAndRequiresRemovableSource(t *testing.T) {
	t.Parallel()
	pending := llm.UserMessage{Role: llm.RoleUser, Timestamp: 1, Content: []llm.ContentPart{llm.NewTextContent(strings.Repeat("x", 12000)).Part()}}
	settings := session.CompactionSettings{KeepRecentTokens: 20000, MaxRetainedTokens: 1000}
	messages := append(textMessages(), pending)
	cut, err := session.PrepareContextCompaction(messages, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cut.RetainedMessages, []llm.AgentMessage{pending}) {
		t.Fatal("oversized unanswered input was summarized")
	}
	summary, err := session.NewContextSummary("prior work", 10, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, context := range [][]llm.AgentMessage{{pending}, {summary, pending}, {summary}} {
		if _, err := session.PrepareContextCompaction(context, settings); !errors.Is(err, session.ErrNothingToCompact) {
			t.Fatalf("context=%#v error=%v", context, err)
		}
	}
}

func TestContextCompactionRejectsIncompleteAndMalformedToolGroups(t *testing.T) {
	t.Parallel()
	settings := session.CompactionSettings{KeepRecentTokens: 1}
	messages := toolMessages()
	if _, err := session.PrepareContextCompaction(messages[:2], settings); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("incomplete group error=%v", err)
	}
	malformed := messages[1].(llm.AssistantMessage)
	malformed.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall}}
	if _, err := session.PrepareContextCompaction([]llm.AgentMessage{messages[0], malformed}, settings); err == nil {
		t.Fatal("nil call accepted")
	}
}

func TestContextCompactionBudgetsFullRetryProjection(t *testing.T) {
	t.Parallel()
	messages := toolMessages()
	failed := messages[1].(llm.AssistantMessage)
	failed.StopReason = llm.StopReasonError
	failed.ErrorMessage = "temporary failure"
	failed.Content = append(failed.Content, llm.NewTextContent(strings.Repeat("x", 20000)).Part())
	paired := messages[2].(llm.ToolResultMessage)
	paired.IsError = true
	succeeded := messages[3]
	history := append(textMessages(), messages[0], failed, paired, succeeded)
	// Failed output exceeds the cap by itself but is absent from the complete
	// model projection. Keep its paired source group together with the successor.
	cut, err := session.PrepareContextCompaction(history, session.CompactionSettings{KeepRecentTokens: 3, MaxRetainedTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if cut.FirstKeptIndex != 2 {
		t.Fatalf("cut index=%d, want retain whole recent retry history", cut.FirstKeptIndex)
	}
	projected, err := llm.AgentMessagesToMessages(cut.RetainedMessages)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 2 {
		t.Fatalf("retained projection=%#v", projected)
	}
	// A subsequent round may reuse the provider's call ID; neither result is orphaned.
	repeat := append(history, messages[1:3]...)
	if _, err := session.PrepareContextCompaction(repeat, session.CompactionSettings{KeepRecentTokens: 1}); err != nil {
		t.Fatal(err)
	}
}
