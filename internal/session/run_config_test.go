package session_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func testRunConfig(t *testing.T, systemPrompt string) session.RunConfig {
	t.Helper()
	record, err := session.NewRunConfig("test-build", llm.RequestConfig{
		Model:        llm.Model{ID: "model", Provider: "provider", ContextWindow: 10_000, MaxTokens: 1_000},
		SystemPrompt: systemPrompt,
		Tools:        []llm.ToolDefinition{{Name: "read", Description: "read a file", InputSchema: []byte(`{"type":"object"}`)}},
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRunConfigIsContentAddressedAndReplayed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	first := testRunConfig(t, "first")
	if again := testRunConfig(t, "first"); again.ID != first.ID {
		t.Fatalf("equal content ids differ: %q != %q", again.ID, first.ID)
	}
	second := testRunConfig(t, "second")
	if second.ID == first.ID {
		t.Fatal("different content shares an id")
	}
	for _, record := range []session.RunConfig{first, first, second} {
		if err := store.AppendRunConfig(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	if lines := strings.Split(strings.TrimSpace(string(fileBytes(t, path))), "\n"); len(lines) != 3 {
		t.Fatalf("records = %d, want header and two distinct configs", len(lines))
	}
	leaf, err := store.LeafID()
	if err != nil || leaf != "" {
		t.Fatalf("leaf = %q, %v; run configs must not move the branch", leaf, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := snapshotOf(t, reopened).RunConfigs; !reflect.DeepEqual(got, []session.RunConfig{first, second}) {
		t.Fatalf("replayed run configs = %#v", got)
	}
}

func TestRunConfigRejectsTamperedContent(t *testing.T) {
	t.Parallel()
	record := testRunConfig(t, "original")
	record.Config.SystemPrompt = "tampered"
	if err := record.Validate(); err == nil {
		t.Fatal("Validate() accepted content that does not match its id")
	}
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	if err := store.AppendRunConfig(t.Context(), record); err == nil {
		t.Fatal("AppendRunConfig() accepted a tampered record")
	}
}

func TestAssistantMessageMustNameRecordedRunConfig(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	conversation := textMessages()
	user := appendMessages(t, store, "user", conversation[0])
	record := testRunConfig(t, "system")
	answer := conversation[1].(llm.AssistantMessage)
	answer.RequestConfig = record.ID
	entry, err := session.NewMessage("assistant", user[0].ID, 101, answer)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(t.Context(), entry); err == nil {
		t.Fatal("AppendMessage() accepted an unrecorded run config")
	}
	if err := store.AppendRunConfig(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}

	snapshot := snapshotOf(t, store)
	request, err := session.RebuildRequest(snapshot, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.SystemPrompt != "system" || request.Model.ID != "model" || len(request.Tools) != 1 {
		t.Fatalf("rebuilt request config = %#v", request)
	}
	if len(request.Messages) != 1 {
		t.Fatalf("rebuilt messages = %#v, want only the parent context", request.Messages)
	}
	if _, err := session.RebuildRequest(snapshot, user[0].ID); err == nil {
		t.Fatal("RebuildRequest() accepted a user message")
	}
	unrecorded := appendMessages(t, store, "plain", conversation[0], conversation[1])[1:]
	if _, err := session.RebuildRequest(snapshotOf(t, store), unrecorded[0].ID); !errors.Is(err, session.ErrNoRunConfig) {
		t.Fatalf("RebuildRequest() error = %v, want ErrNoRunConfig", err)
	}
}

func TestRebuildRequestFollowsEachBranch(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	conversation := textMessages()
	root := appendMessages(t, store, "root", conversation[0], conversation[1])
	first, second := testRunConfig(t, "first"), testRunConfig(t, "second")
	answers := make([]string, 0, 2)
	for i, record := range []session.RunConfig{first, second} {
		if i == 1 {
			moveTo(t, store, "leaf-back", root[1].ID)
		}
		appendMessages(t, store, fmt.Sprintf("prompt%d", i), conversation[0])
		if err := store.AppendRunConfig(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		answer := conversation[1].(llm.AssistantMessage)
		answer.RequestConfig = record.ID
		answers = append(answers, appendMessages(t, store, fmt.Sprintf("answer%d", i), answer)[0].ID)
	}
	snapshot := snapshotOf(t, store)
	for i, want := range []string{"first", "second"} {
		request, err := session.RebuildRequest(snapshot, answers[i])
		if err != nil {
			t.Fatal(err)
		}
		if request.SystemPrompt != want || len(request.Messages) != 3 {
			t.Fatalf("branch %d rebuilt %q with %d messages", i, request.SystemPrompt, len(request.Messages))
		}
	}
}
