package session_test

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestCompactionPreservesSourcesAndRestoresContext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	first := appendMessages(t, store, "first", namedTextMessages("first prompt", "first answer", 10)...)
	second := appendMessages(t, store, "second", namedTextMessages("second prompt", "second answer", 20)...)
	before := fileBytes(t, path)
	checkpoint := mustCompaction(t, session.CompactionInput{
		ID:                   "compact",
		ParentID:             second[1].ID,
		CreatedAt:            250,
		Summary:              "first summarized",
		TokensBefore:         20,
		FirstKeptMessageID:   second[0].ID,
		ActiveMessageCount:   4,
		RetainedMessageCount: 2,
		Usage: llm.Usage{
			TotalTokens: 16,
			Cost: &llm.Cost{
				Total: 0.01,
			},
		},
	})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	third := appendMessages(t, store, "third", namedTextMessages("third prompt", "third answer", 30)...)
	if !strings.HasPrefix(string(fileBytes(t, path)), string(before)) {
		t.Fatal("source bytes replaced")
	}
	store.Close()
	store, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := snapshotOf(t, store)
	want := append(append(first, second...), third...)
	if !reflect.DeepEqual(snapshot.Messages, want) {
		t.Fatal("source records changed")
	}
	contextMessages, err := session.BuildContext(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(contextMessages) != 5 {
		t.Fatalf("context=%v", contextMessages)
	}
	if summary, ok := contextMessages[0].(llm.CompactionSummaryMessage); !ok || summary.Summary != checkpoint.Summary {
		t.Fatal("missing summary")
	}
	assertSessionText(t, contextMessages[1], llm.RoleUser, "second prompt")
	assertSessionText(t, contextMessages[4], llm.RoleAssistant, "third answer")
	projected, err := llm.AgentMessagesToMessages(contextMessages)
	if err != nil {
		t.Fatal(err)
	}
	if estimate := llm.EstimateContextTokens(llm.Request{Messages: projected}); estimate.UsageTokens != 0 {
		t.Fatalf("stale usage reused: %#v", estimate)
	}
	snapshot.Compactions[0].Usage.Cost.Total = 99
	if snapshotOf(t, store).Compactions[0].Usage.Cost.Total != 0.01 {
		t.Fatal("compaction snapshot aliased")
	}
}

func TestPrepareCompactionKeepsPairedRecentGroups(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	appendMessages(t, store, "first", textMessages()...)
	latest := appendMessages(t, store, "latest", toolMessages()[:3]...)
	prep, err := session.PrepareCompaction(snapshotOf(t, store), session.CompactionSettings{KeepRecentTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if prep.FirstKeptMessageID != latest[0].ID || prep.ActiveMessageCount != 5 || prep.RetainedMessageCount != 3 || len(prep.MessagesToSummarize) != 2 {
		t.Fatalf("prep=%#v", prep)
	}
}

func TestPrepareCompactionSplitsOneLongInteractionAndUpdatesSummary(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := toolMessages()
	appendMessages(t, store, "initial", messages[:3]...)
	latest := appendMessages(t, store, "next", messages[1:3]...)
	prep, err := session.PrepareCompaction(snapshotOf(t, store), session.CompactionSettings{KeepRecentTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if prep.FirstKeptMessageID != latest[0].ID || prep.ActiveMessageCount != 5 || prep.RetainedMessageCount != 2 {
		t.Fatalf("prep=%#v", prep)
	}
	checkpoint := mustCompaction(t, session.CompactionInput{
		ID:                   "compact",
		ParentID:             latest[1].ID,
		CreatedAt:            400,
		Summary:              "earlier round",
		TokensBefore:         prep.TokensBefore,
		FirstKeptMessageID:   prep.FirstKeptMessageID,
		ActiveMessageCount:   prep.ActiveMessageCount,
		RetainedMessageCount: prep.RetainedMessageCount,
	})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	final := appendMessages(t, store, "final", messages[3])
	prep, err = session.PrepareCompaction(snapshotOf(t, store), session.CompactionSettings{KeepRecentTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if prep.FirstKeptMessageID != final[0].ID || prep.ActiveMessageCount != 3 || prep.RetainedMessageCount != 1 || len(prep.MessagesToSummarize) != 3 {
		t.Fatalf("repeat prep=%#v", prep)
	}
	if summary, ok := prep.MessagesToSummarize[0].(llm.CompactionSummaryMessage); !ok || summary.Summary != "earlier round" {
		t.Fatal("previous summary lost")
	}
}

func TestCompactionRejectsInvalidBoundaryOnAppendAndReplay(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"pending parent", "inside group", "wrong count", "wrong branch"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			store := mustCreate(t, path)
			messages := toolMessages()
			entries := appendMessages(t, store, "initial", messages...)
			input := session.CompactionInput{
				ID:                   "bad",
				ParentID:             entries[3].ID,
				CreatedAt:            400,
				Summary:              "invalid",
				TokensBefore:         100,
				FirstKeptMessageID:   entries[2].ID,
				ActiveMessageCount:   4,
				RetainedMessageCount: 2,
			}
			switch kind {
			case "pending parent":
				pending := appendMessages(t, store, "pending", messages[1])
				input.ParentID = pending[0].ID
				input.ActiveMessageCount = 5
				input.RetainedMessageCount = 3
			case "wrong count":
				input.ActiveMessageCount = 6
			case "wrong branch":
				input.FirstKeptMessageID = "missing"
			}
			checkpoint := mustCompaction(t, input)
			before := fileBytes(t, path)
			if err := store.AppendCompaction(t.Context(), checkpoint); err == nil {
				t.Fatal("invalid compaction accepted")
			}
			if !reflect.DeepEqual(fileBytes(t, path), before) {
				t.Fatal("invalid compaction wrote")
			}
			store.Close()
			data, err := json.Marshal(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			appendBytes(t, path, append(data, '\n'))
			if _, err := session.Open(t.Context(), path); !errors.Is(err, session.ErrCorrupt) {
				t.Fatalf("replay=%v", err)
			}
		})
	}
}

func TestCompactionFullFallbackAndFutureContext(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := namedTextMessages("inspect", strings.Repeat("large output ", 8000), 0)
	appendMessages(t, store, "large", messages...)
	prep, err := session.PrepareCompaction(snapshotOf(t, store), session.CompactionSettings{KeepRecentTokens: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if prep.FirstKeptMessageID != "" || prep.ActiveMessageCount != 2 || prep.RetainedMessageCount != 0 || len(prep.MessagesToSummarize) != 2 {
		t.Fatalf("prep=%#v", prep)
	}
	parent, _ := store.LeafID()
	checkpoint := mustCompaction(t, session.CompactionInput{
		ID:                 "compact",
		ParentID:           parent,
		CreatedAt:          300,
		Summary:            "summary",
		TokensBefore:       prep.TokensBefore,
		ActiveMessageCount: 2,
	})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	appendMessages(t, store, "next", textMessages()...)
	contextMessages, err := session.BuildContext(snapshotOf(t, store))
	if err != nil || len(contextMessages) != 3 {
		t.Fatalf("context=%v err=%v", contextMessages, err)
	}
}

func TestPrepareCompactionNothingAndTimestampOverflow(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	appendMessages(t, store, "first", textMessages()...)
	settings := session.CompactionSettings{KeepRecentTokens: 20000}
	if _, err := session.PrepareCompaction(snapshotOf(t, store), settings); !errors.Is(err, session.ErrNothingToCompact) {
		t.Fatal(err)
	}
	a := textMessages()[1].(llm.AssistantMessage)
	a.Timestamp = math.MaxInt64
	retained := appendMessages(t, store, "last", a)
	checkpoint := mustCompaction(t, session.CompactionInput{
		ID:                   "compact",
		ParentID:             retained[0].ID,
		CreatedAt:            200,
		Summary:              "summary",
		TokensBefore:         100,
		FirstKeptMessageID:   retained[0].ID,
		ActiveMessageCount:   3,
		RetainedMessageCount: 1,
	})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := session.BuildContext(snapshotOf(t, store)); err == nil {
		t.Fatal("timestamp overflow accepted")
	}
}
