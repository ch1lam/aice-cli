package session_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestStoreAppendsCompactionWithoutReplacingTurns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	first := mustTurn(
		t,
		"turn-1",
		"",
		1_721_234_567_900,
		namedTextMessages("first prompt", "first answer", 10),
	)
	second := mustTurn(
		t,
		"turn-2",
		first.ID,
		1_721_234_568_000,
		namedTextMessages("second prompt", "second answer", 20),
	)
	if err := store.AppendTurn(t.Context(), first); err != nil {
		t.Fatalf("AppendTurn(first) error = %v", err)
	}
	if err := store.AppendTurn(t.Context(), second); err != nil {
		t.Fatalf("AppendTurn(second) error = %v", err)
	}
	compaction := mustCompaction(t, session.CompactionInput{
		ID:                "compaction-1",
		ParentID:          second.ID,
		CreatedAt:         1_721_234_568_050,
		Summary:           "The first turn established the project goal.",
		TokensBefore:      20,
		FirstKeptTurnID:   second.ID,
		ActiveTurnCount:   2,
		RetainedTurnCount: 1,
		Usage: llm.Usage{
			InputTokens:  12,
			OutputTokens: 4,
			TotalTokens:  16,
			Cost:         &llm.Cost{Total: 0.01},
		},
	})
	if err := store.AppendCompaction(t.Context(), compaction); err != nil {
		t.Fatalf("AppendCompaction() error = %v", err)
	}
	third := mustTurn(
		t,
		"turn-3",
		compaction.ID,
		1_721_234_568_100,
		namedTextMessages("third prompt", "third answer", 30),
	)
	if err := store.AppendTurn(t.Context(), third); err != nil {
		t.Fatalf("AppendTurn(third) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	snapshot, err := reopened.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if want := []session.Turn{first, second, third}; !reflect.DeepEqual(snapshot.Turns, want) {
		t.Fatalf("original turns = %#v, want %#v", snapshot.Turns, want)
	}
	if want := []session.Compaction{compaction}; !reflect.DeepEqual(snapshot.Compactions, want) {
		t.Fatalf("compactions = %#v, want %#v", snapshot.Compactions, want)
	}
	snapshot.Compactions[0].Usage.Cost.Total = 99
	freshSnapshot, err := reopened.Snapshot()
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	if got := freshSnapshot.Compactions[0].Usage.Cost.Total; got != 0.01 {
		t.Fatalf("stored compaction cost after snapshot mutation = %v, want 0.01", got)
	}

	contextMessages, err := session.BuildContext(snapshot)
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if len(contextMessages) != 5 {
		t.Fatalf("context messages = %d, want summary plus two complete turns", len(contextMessages))
	}
	summary, ok := contextMessages[0].(llm.CompactionSummaryMessage)
	if !ok || summary.Summary != compaction.Summary {
		t.Fatalf("context summary = %#v", contextMessages[0])
	}
	assertSessionText(t, contextMessages[1], llm.RoleUser, "second prompt")
	assertSessionText(t, contextMessages[2], llm.RoleAssistant, "second answer")
	assertSessionText(t, contextMessages[3], llm.RoleUser, "third prompt")
	assertSessionText(t, contextMessages[4], llm.RoleAssistant, "third answer")
	projected, err := llm.AgentMessagesToMessages(contextMessages)
	if err != nil {
		t.Fatalf("AgentMessagesToMessages() error = %v", err)
	}
	estimate := llm.EstimateContextTokens(llm.Request{Messages: projected})
	if estimate.UsageTokens != 0 {
		t.Fatalf(
			"compacted context reused pre-compaction usage: %#v",
			estimate,
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var recordTypes []session.RecordType
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var envelope struct {
			Type session.RecordType `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("decode record type error = %v", err)
		}
		recordTypes = append(recordTypes, envelope.Type)
	}
	wantTypes := []session.RecordType{
		session.RecordTypeSession,
		session.RecordTypeTurn,
		session.RecordTypeTurn,
		session.RecordTypeCompaction,
		session.RecordTypeTurn,
	}
	if !reflect.DeepEqual(recordTypes, wantTypes) {
		t.Fatalf("record types = %v, want %v", recordTypes, wantTypes)
	}
}

func TestStoreRejectsCompactionOutsideCurrentBoundary(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if err := store.AppendTurn(
		t.Context(),
		mustTurn(t, "turn-1", "", 1_721_234_567_900, textMessages()),
	); err != nil {
		t.Fatalf("AppendTurn() error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	compaction := mustCompaction(t, session.CompactionInput{
		ID:                "compaction-1",
		ParentID:          "turn-1",
		CreatedAt:         1_721_234_568_000,
		Summary:           "summary",
		TokensBefore:      15,
		FirstKeptTurnID:   "turn-1",
		ActiveTurnCount:   2,
		RetainedTurnCount: 1,
	})

	err = store.AppendCompaction(t.Context(), compaction)
	if err == nil || !strings.Contains(err.Error(), "active turn count") {
		t.Fatalf("AppendCompaction() error = %v, want branch-boundary error", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() after rejection error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("file size after rejection = %d, want %d", after.Size(), before.Size())
	}
}

func TestStoreOpenRejectsCompactionOutsideRecordedBoundary(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	if err := store.AppendTurn(
		t.Context(),
		mustTurn(t, "turn-1", "", 1_721_234_567_900, textMessages()),
	); err != nil {
		t.Fatalf("AppendTurn() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	compaction := mustCompaction(t, session.CompactionInput{
		ID:                "compaction-1",
		ParentID:          "turn-1",
		CreatedAt:         1_721_234_568_000,
		Summary:           "summary",
		TokensBefore:      15,
		FirstKeptTurnID:   "turn-1",
		ActiveTurnCount:   2,
		RetainedTurnCount: 1,
	})
	data, err := json.Marshal(compaction)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() appended file error = %v", err)
	}

	_, err = session.Open(t.Context(), path)
	if !errors.Is(err, session.ErrCorrupt) {
		t.Fatalf("Open() error = %v, want ErrCorrupt", err)
	}
}

func TestPrepareCompactionKeepsCompleteRecentTurns(t *testing.T) {
	t.Parallel()

	first := mustTurn(
		t,
		"turn-1",
		"",
		100,
		namedTextMessages("first prompt", "first answer", 10),
	)
	second := mustTurn(
		t,
		"turn-2",
		first.ID,
		200,
		namedTextMessages("second prompt", "second answer", 20),
	)
	third := mustTurn(
		t,
		"turn-3",
		second.ID,
		300,
		namedTextMessages("third prompt", "third answer", 30),
	)
	snapshot := session.Snapshot{
		Turns:  []session.Turn{first, second, third},
		Order:  []string{first.ID, second.ID, third.ID},
		LeafID: third.ID,
	}
	preparation, err := session.PrepareCompaction(snapshot, session.CompactionSettings{
		KeepRecentTokens: 1,
	})
	if err != nil {
		t.Fatalf("PrepareCompaction() error = %v", err)
	}
	if preparation.FirstKeptTurnID != third.ID ||
		preparation.ActiveTurnCount != 3 ||
		preparation.RetainedTurnCount != 1 {
		t.Fatalf("preparation boundary = %#v", preparation)
	}
	if len(preparation.MessagesToSummarize) != 4 {
		t.Fatalf(
			"messages to summarize = %d, want two complete turns",
			len(preparation.MessagesToSummarize),
		)
	}
	assertSessionText(t, preparation.MessagesToSummarize[0], llm.RoleUser, "first prompt")
	assertSessionText(t, preparation.MessagesToSummarize[3], llm.RoleAssistant, "second answer")
}

func TestPrepareCompactionUpdatesPreviousSummary(t *testing.T) {
	t.Parallel()

	first := mustTurn(
		t,
		"turn-1",
		"",
		100,
		namedTextMessages("first prompt", "first answer", 10),
	)
	second := mustTurn(
		t,
		"turn-2",
		first.ID,
		200,
		namedTextMessages("second prompt", "second answer", 20),
	)
	previous := mustCompaction(t, session.CompactionInput{
		ID:                "compaction-1",
		ParentID:          second.ID,
		CreatedAt:         250,
		Summary:           "The first turn established the project goal.",
		TokensBefore:      20,
		FirstKeptTurnID:   second.ID,
		ActiveTurnCount:   2,
		RetainedTurnCount: 1,
	})
	third := mustTurn(
		t,
		"turn-3",
		previous.ID,
		300,
		namedTextMessages("third prompt", "third answer", 30),
	)
	preparation, err := session.PrepareCompaction(session.Snapshot{
		Turns:       []session.Turn{first, second, third},
		Compactions: []session.Compaction{previous},
		Order:       []string{first.ID, second.ID, previous.ID, third.ID},
		LeafID:      third.ID,
	}, session.CompactionSettings{KeepRecentTokens: 1})
	if err != nil {
		t.Fatalf("PrepareCompaction() error = %v", err)
	}
	if preparation.FirstKeptTurnID != third.ID {
		t.Fatalf(
			"FirstKeptTurnID = %q, want %q",
			preparation.FirstKeptTurnID,
			third.ID,
		)
	}
	if len(preparation.MessagesToSummarize) != 3 {
		t.Fatalf(
			"messages to summarize = %d, want prior summary and second turn",
			len(preparation.MessagesToSummarize),
		)
	}
	summary, ok := preparation.MessagesToSummarize[0].(llm.CompactionSummaryMessage)
	if !ok || summary.Summary != previous.Summary {
		t.Fatalf("previous summary message = %#v", preparation.MessagesToSummarize[0])
	}
	assertSessionText(t, preparation.MessagesToSummarize[1], llm.RoleUser, "second prompt")
	assertSessionText(t, preparation.MessagesToSummarize[2], llm.RoleAssistant, "second answer")
}

func TestPrepareCompactionReportsNothingToCompact(t *testing.T) {
	t.Parallel()

	only := mustTurn(t, "turn-1", "", 100, textMessages())
	_, err := session.PrepareCompaction(session.Snapshot{
		Turns:  []session.Turn{only},
		Order:  []string{only.ID},
		LeafID: only.ID,
	}, session.CompactionSettings{KeepRecentTokens: 20_000})
	if !errors.Is(err, session.ErrNothingToCompact) {
		t.Fatalf("PrepareCompaction() error = %v, want ErrNothingToCompact", err)
	}
}

func mustCompaction(
	t *testing.T,
	input session.CompactionInput,
) session.Compaction {
	t.Helper()

	compaction, err := session.NewCompaction(input)
	if err != nil {
		t.Fatalf("NewCompaction() error = %v", err)
	}
	return compaction
}

func namedTextMessages(
	prompt string,
	answer string,
	usageTokens int64,
) []llm.AgentMessage {
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent(prompt).Part()},
			Timestamp: usageTokens*10 + 1,
		},
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			Content:    []llm.ContentPart{llm.NewTextContent(answer).Part()},
			API:        "custom-chat-api",
			Provider:   "custom-provider",
			ModelID:    "requested-model",
			Usage:      llm.Usage{TotalTokens: usageTokens},
			StopReason: llm.StopReasonStop,
			Timestamp:  usageTokens*10 + 2,
		},
	}
}

func assertSessionText(
	t *testing.T,
	message llm.AgentMessage,
	role llm.Role,
	text string,
) {
	t.Helper()

	switch value := message.(type) {
	case llm.UserMessage:
		if role != llm.RoleUser || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want user text %q", message, text)
		}
	case llm.AssistantMessage:
		if role != llm.RoleAssistant || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want assistant text %q", message, text)
		}
	default:
		t.Errorf("message = %#v, want role %q text %q", message, role, text)
	}
}
