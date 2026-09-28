package session_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestToolDetailsSurviveBranchesCompactionAndReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	messages := toolMessages()
	result := messages[2].(llm.ToolResultMessage)
	result.Content = []llm.ContentPart{llm.NewTextContent("before").Part(), {Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, llm.NewTextContent("after").Part()}
	result.IsError = true
	result.Details = &llm.ToolResultDetails{State: llm.ExecutionReturned, StructuredContent: json.RawMessage(" \n{\n  \"value\": 9007199254740993, \"label\": \"<tag>&\", \"value\": 1.20e+03\n}\t"), Binding: &llm.ToolBinding{Source: "project:/fixture", ServiceID: "test", ConnectionFingerprint: "connection", ToolName: "inspect", SchemaFingerprint: "schema"}, Loss: "one block exceeded hard limit"}
	messages[2] = result
	entries := appendMessages(t, store, "initial", messages...)
	original := fileBytes(t, path)
	checkpoint := mustCompaction(t, session.CompactionInput{ID: "compact", ParentID: entries[3].ID, CreatedAt: 400, Summary: "summarized", TokensBefore: 100, ActiveMessageCount: 4})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	leaf, err := session.NewLeaf("branch-leaf", "compact", entries[2].ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	appendMessages(t, store, "branch", messages[3])
	if !strings.HasPrefix(string(fileBytes(t, path)), string(original)) {
		t.Fatal("source rewritten")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot := snapshotOf(t, reopened)
	got := snapshot.Messages[2].Message.(llm.ToolResultMessage)
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("source result changed: %#v", got)
	}
	history, err := session.BuildContext(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history[2], result) {
		t.Fatal("branch lost ordered result")
	}
	got.Details.StructuredContent[0] = '!'
	if !json.Valid(snapshotOf(t, reopened).Messages[2].Message.(llm.ToolResultMessage).Details.StructuredContent) {
		t.Fatal("snapshot aliases storage")
	}
}
