package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestStoreCreateAppendAndReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "session.jsonl")
	metadata := session.Metadata{
		ID:               "session-1",
		CreatedAt:        1_721_234_567_800,
		WorkingDirectory: t.TempDir(),
	}
	store, err := session.Create(t.Context(), path, metadata)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	textTurn := mustTurn(t, 1_721_234_567_900, textMessages())
	toolTurn := mustTurn(t, 1_721_234_568_000, toolMessages())
	if err := store.AppendTurn(t.Context(), textTurn); err != nil {
		t.Fatalf("AppendTurn(text) error = %v", err)
	}
	if err := store.AppendTurn(t.Context(), toolTurn); err != nil {
		t.Fatalf("AppendTurn(tool) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("session mode = %o, want no group or other permissions", info.Mode().Perm())
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
	wantHeader := session.Header{
		Type:             session.RecordTypeSession,
		Version:          session.CurrentVersion,
		ID:               metadata.ID,
		CreatedAt:        metadata.CreatedAt,
		WorkingDirectory: metadata.WorkingDirectory,
	}
	if !reflect.DeepEqual(snapshot.Header, wantHeader) {
		t.Fatalf("snapshot header = %#v, want %#v", snapshot.Header, wantHeader)
	}
	if want := []session.Turn{textTurn, toolTurn}; !reflect.DeepEqual(snapshot.Turns, want) {
		t.Fatalf("snapshot turns = %#v, want %#v", snapshot.Turns, want)
	}
	user := snapshot.Turns[0].Messages[0].(llm.UserMessage)
	user.Content[0].Text = "mutated snapshot"
	snapshot.Turns[0].Messages[0] = user
	freshSnapshot, err := reopened.Snapshot()
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	freshUser := freshSnapshot.Turns[0].Messages[0].(llm.UserMessage)
	if got, want := freshUser.Content[0].Text, "hello"; got != want {
		t.Fatalf("stored message after snapshot mutation = %q, want %q", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("session line count = %d, want header and two turns", len(lines))
	}
	for index, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("session line %d is invalid JSON: %q", index+1, line)
		}
	}
}

func TestStoreOpenTruncatesIncompleteTail(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	first := mustTurn(t, 1_721_234_567_900, textMessages())
	if err := store.AppendTurn(t.Context(), first); err != nil {
		t.Fatalf("AppendTurn() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}
	if _, err := file.WriteString(`{"type":"turn","completed_at":`); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() partial file error = %v", err)
	}

	recovered, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	snapshot, err := recovered.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if want := []session.Turn{first}; !reflect.DeepEqual(snapshot.Turns, want) {
		t.Fatalf("recovered turns = %#v, want %#v", snapshot.Turns, want)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() after recovery error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("recovered size = %d, want %d", after.Size(), before.Size())
	}

	second := mustTurn(t, 1_721_234_568_000, textMessages())
	if err := recovered.AppendTurn(t.Context(), second); err != nil {
		t.Fatalf("AppendTurn() after recovery error = %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open() after append error = %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	snapshot, err = reopened.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() after append error = %v", err)
	}
	if len(snapshot.Turns) != 2 {
		t.Fatalf("reopened turn count = %d, want 2", len(snapshot.Turns))
	}
}

func TestStoreOpenRejectsCompleteCorruptRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}
	if _, err := file.WriteString("{not-json}\n"); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() corrupt file error = %v", err)
	}

	_, err = session.Open(t.Context(), path)
	if !errors.Is(err, session.ErrCorrupt) {
		t.Fatalf("Open() error = %v, want ErrCorrupt", err)
	}
}

func TestStoreOpenRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","version":2,"id":"future","created_at":1721234567800,` +
		`"working_directory":"/tmp"}` + "\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := session.Open(t.Context(), path)
	if !errors.Is(err, session.ErrUnsupportedVersion) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestStoreRejectsIncompleteTurnWithoutChangingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	incomplete := session.Turn{
		Type:        session.RecordTypeTurn,
		CompletedAt: 1_721_234_567_900,
		Messages: []llm.AgentMessage{
			textMessages()[0],
			toolMessages()[1],
		},
		Usage: toolMessages()[1].(llm.AssistantMessage).Usage,
	}

	err = store.AppendTurn(t.Context(), incomplete)
	if err == nil || !strings.Contains(err.Error(), "unpaired tool call") {
		t.Fatalf("AppendTurn() error = %v, want incomplete-turn error", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() after invalid append error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("file size after invalid append = %d, want %d", after.Size(), before.Size())
	}
}

func TestStoreRejectsDerivedMessageInsideTurn(t *testing.T) {
	t.Parallel()

	_, err := session.NewTurn(1_721_234_567_900, []llm.AgentMessage{
		textMessages()[0],
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "derived context",
			TokensBefore: 100,
			Timestamp:    1_721_234_567_850,
		},
		textMessages()[1],
	})
	if err == nil || !strings.Contains(err.Error(), "derived message") {
		t.Fatalf("NewTurn() error = %v, want derived-message rejection", err)
	}
}

func TestStoreHonorsCancellationAndClosedState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	turn := mustTurn(t, 1_721_234_567_900, textMessages())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.AppendTurn(ctx, turn); !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendTurn() error = %v, want context.Canceled", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := store.AppendTurn(t.Context(), turn); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("AppendTurn() after close error = %v, want ErrClosed", err)
	}
}

func mustCreate(t *testing.T, path string) *session.Store {
	t.Helper()

	store, err := session.Create(t.Context(), path, session.Metadata{
		ID:               "session-1",
		CreatedAt:        1_721_234_567_800,
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store
}

func mustTurn(t *testing.T, completedAt int64, messages []llm.AgentMessage) session.Turn {
	t.Helper()

	turn, err := session.NewTurn(completedAt, messages)
	if err != nil {
		t.Fatalf("NewTurn() error = %v", err)
	}
	return turn
}

func textMessages() []llm.AgentMessage {
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("hello").Part()},
			Timestamp: 1_721_234_567_810,
		},
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			Content:    []llm.ContentPart{llm.NewTextContent("hello back").Part()},
			API:        "custom-chat-api",
			Provider:   "custom-provider",
			ModelID:    "requested-model",
			ResponseID: "response-text",
			Usage: llm.Usage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
			StopReason: llm.StopReasonStop,
			Timestamp:  1_721_234_567_820,
		},
	}
}

func toolMessages() []llm.AgentMessage {
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
		Signature: "tool-signature",
	}
	return []llm.AgentMessage{
		llm.UserMessage{
			Role:      llm.RoleUser,
			Content:   []llm.ContentPart{llm.NewTextContent("inspect").Part()},
			Timestamp: 1_721_234_567_910,
		},
		llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				llm.NewThinkingContent("reasoning", "thinking-signature").Part(),
				{Type: llm.ContentTypeToolCall, ToolCall: &call},
			},
			API:             "custom-chat-api",
			Provider:        "custom-provider",
			ModelID:         "requested-model",
			ResponseModelID: "resolved-model",
			ResponseID:      "response-tool",
			Usage: llm.Usage{
				InputTokens:      20,
				OutputTokens:     10,
				ReasoningTokens:  4,
				CacheReadTokens:  3,
				CacheWriteTokens: 2,
				TotalTokens:      30,
				Cost: &llm.Cost{
					Input:      0.001,
					Output:     0.002,
					CacheRead:  0.0001,
					CacheWrite: 0.0002,
					Total:      0.0033,
				},
			},
			StopReason:   llm.StopReasonToolUse,
			ErrorMessage: "redacted provider diagnostic",
			Timestamp:    1_721_234_567_920,
		},
		llm.ToolResultMessage{
			Role:       llm.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    []llm.ContentPart{llm.NewTextContent("contents").Part()},
			Timestamp:  1_721_234_567_930,
		},
		llm.AssistantMessage{
			Role:            llm.RoleAssistant,
			Content:         []llm.ContentPart{llm.NewTextContent("done").Part()},
			API:             "custom-chat-api",
			Provider:        "custom-provider",
			ModelID:         "requested-model",
			ResponseModelID: "resolved-model",
			ResponseID:      "response-final",
			Usage: llm.Usage{
				InputTokens:  40,
				OutputTokens: 8,
				TotalTokens:  48,
				Cost: &llm.Cost{
					Input:  0.004,
					Output: 0.001,
					Total:  0.005,
				},
			},
			StopReason: llm.StopReasonStop,
			Timestamp:  1_721_234_567_940,
		},
	}
}
