package session_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestTotalUsageIncludesSourceAssistantsAndAllCompactions(t *testing.T) {
	t.Parallel()
	a := textMessages()[1].(llm.AssistantMessage)
	a.Usage = llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120, Cost: &llm.Cost{Input: 0.01, Total: 0.01}}
	b := a
	b.Usage = llm.Usage{InputTokens: 40, OutputTokens: 10, CacheReadTokens: 30, TotalTokens: 80, Cost: &llm.Cost{Output: 0.02, CacheRead: 0.001, Total: 0.021}}
	snapshot := session.Snapshot{
		Messages: []session.MessageEntry{
			{
				ID:      "active",
				Message: a,
			},
			{
				ID:      "abandoned",
				Message: b,
			},
			{
				ID:      "user",
				Message: textMessages()[0],
			},
		},
		Compactions: []session.Compaction{
			{
				Usage: llm.Usage{
					InputTokens:  60,
					OutputTokens: 15,
					TotalTokens:  75,
					Cost: &llm.Cost{
						Output: 0.003,
						Total:  0.003,
					},
				},
			},
		},
	}
	want := llm.Usage{
		InputTokens:     200,
		OutputTokens:    45,
		CacheReadTokens: 30,
		TotalTokens:     275,
		Cost: &llm.Cost{
			Input:     0.01,
			Output:    0.023,
			CacheRead: 0.001,
			Total:     0.034,
		},
	}
	if got := session.TotalUsage(snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("usage=%#v", got)
	}
}

func TestNewMessageRejectsDerivedAndMalformedMessages(t *testing.T) {
	t.Parallel()
	summary, _ := llm.NewCompactionSummaryMessage("derived", 100)
	malformed := textMessages()[1].(llm.AssistantMessage)
	malformed.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall}}
	for _, message := range []llm.AgentMessage{nil, summary, malformed} {
		if _, err := session.NewMessage("id", "", 100, message); err == nil {
			t.Fatalf("invalid source %T accepted", message)
		}
	}
	var entry session.MessageEntry
	for _, raw := range []string{
		`null`,
		`{"type":"message","id":"m","created_at":1,"message":null}`,
		`{"type":"message","id":"m","created_at":1,"message":{"role":"toolResult","toolCallId":"x"}}`,
	} {
		if err := json.Unmarshal([]byte(raw), &entry); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestNewMessageClonesCallerData(t *testing.T) {
	t.Parallel()
	message := toolMessages()[1].(llm.AssistantMessage)
	entry, err := session.NewMessage("id", "parent", 100, message)
	if err != nil {
		t.Fatal(err)
	}
	message.Usage.Cost.Total = 99
	message.Content[len(message.Content)-1].ToolCall.Arguments[0] = '!'
	got := entry.Message.(llm.AssistantMessage)
	if got.Usage.Cost.Total == 99 || !json.Valid(got.Content[len(got.Content)-1].ToolCall.Arguments) {
		t.Fatal("caller aliases source record")
	}
}

func TestMessageRequiresTerminalAssistant(t *testing.T) {
	t.Parallel()
	for _, reason := range []llm.StopReason{
		llm.StopReasonUnknown, "unrecognized", llm.StopReasonStop, llm.StopReasonLength,
		llm.StopReasonToolUse, llm.StopReasonPause, llm.StopReasonRefusal,
		llm.StopReasonError, llm.StopReasonAborted,
	} {
		assistant := textMessages()[1].(llm.AssistantMessage)
		assistant.StopReason = reason
		_, err := session.NewMessage("assistant", "user", 100, assistant)
		wantError := reason == llm.StopReasonUnknown || reason == "unrecognized"
		if (err != nil) != wantError {
			t.Fatalf("reason=%q error=%v", reason, err)
		}
	}
}

func TestSnapshotSiblingPrefixesHaveIndependentPendingCalls(t *testing.T) {
	t.Parallel()
	messages := toolMessages()
	assistant := messages[1].(llm.AssistantMessage)
	second := *assistant.Content[len(assistant.Content)-1].ToolCall
	second.ID = "call-2"
	assistant.Content = append(assistant.Content, llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &second})
	user, _ := session.NewMessage("user", "", 100, messages[0])
	call, _ := session.NewMessage("calls", "user", 101, assistant)
	left, _ := session.NewMessage("left-result", "calls", 102, messages[2])
	right, _ := session.NewMessage("right-result", "calls", 103, messages[2])
	// A reader can inspect historical prefixes independently. Consuming a call
	// in one branch must not consume it in the parent or a sibling's validation.
	snapshot := session.Snapshot{
		Messages: []session.MessageEntry{
			user,
			call,
			left,
			right,
		},
		Order: []string{
			"user",
			"calls",
			"left-result",
			"right-result",
		},
		LeafID: "right-result",
	}
	if _, err := session.Nodes(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{"calls", "left-result", "right-result"} {
		snapshot.LeafID = leaf
		if _, err := session.BuildContext(snapshot); err == nil {
			t.Fatalf("pending prefix %q accepted", leaf)
		}
	}
}
