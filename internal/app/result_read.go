package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type resultReaderContextKey struct{}
type resultReaderFunc func(context.Context, string, string) (tool.StoredToolResult, error)

// The built-in holds no shared mutable Session pointer. Each main run supplies
// its own capability; a model cannot nominate a file, Session or inactive leaf.
type runResultReader struct{}

func (runResultReader) ReadToolResult(ctx context.Context, callID, entryID string) (tool.StoredToolResult, error) {
	reader, ok := ctx.Value(resultReaderContextKey{}).(resultReaderFunc)
	if !ok {
		return tool.StoredToolResult{}, fmt.Errorf("result readback requires an active main run")
	}
	return reader(ctx, callID, entryID)
}

// activeEntries returns the run's active branch: the Session's when one is
// stored, otherwise the print run's own source messages.
func activeEntries(store *session.Store, memory func() []llm.AgentMessage) ([]session.MessageEntry, error) {
	var entries []session.MessageEntry
	if store != nil {
		snapshot, err := store.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("Session result snapshot is unavailable")
		}
		branch, err := session.ActiveBranch(snapshot)
		if err != nil {
			return nil, fmt.Errorf("Session branch is unavailable")
		}
		active := make(map[string]bool, len(branch))
		for _, node := range branch {
			active[node.ID] = true
		}
		for _, entry := range snapshot.Messages {
			if active[entry.ID] {
				entries = append(entries, entry)
			}
		}
	} else if memory != nil {
		// --print without --session already retains source messages for image
		// lookup. Reuse that run-owned history, without a second transcript.
		for i, message := range memory() {
			entries = append(entries, session.MessageEntry{ID: fmt.Sprintf("run-%d", i), Message: message})
		}
	}
	return entries, nil
}

type transcriptContextKey struct{}

// runTranscript reads the run's active branch at call time.
type runTranscript func() ([]session.MessageEntry, error)

// transcriptFrom returns the active branch of the run bound to ctx, or nil
// outside a run.
func transcriptFrom(ctx context.Context) ([]session.MessageEntry, error) {
	read, ok := ctx.Value(transcriptContextKey{}).(runTranscript)
	if !ok {
		return nil, nil
	}
	return read()
}

func withResultReader(ctx context.Context, store *session.Store, memory func() []llm.AgentMessage) context.Context {
	ctx = context.WithValue(ctx, transcriptContextKey{}, runTranscript(func() ([]session.MessageEntry, error) { return activeEntries(store, memory) }))
	reader := resultReaderFunc(func(ctx context.Context, callID, entryID string) (tool.StoredToolResult, error) {
		if err := ctx.Err(); err != nil {
			return tool.StoredToolResult{}, err
		}
		entries, err := activeEntries(store, memory)
		if err != nil {
			return tool.StoredToolResult{}, err
		}
		var matches []tool.StoredToolResult
		for _, entry := range entries {
			message, ok := entry.Message.(llm.ToolResultMessage)
			if ok && (entryID != "" && entry.ID == entryID || entryID == "" && message.ToolCallID == callID) {
				matches = append(matches, tool.StoredToolResult{ID: entry.ID, Message: message, Durable: store != nil})
			}
		}
		if len(matches) == 0 {
			return tool.StoredToolResult{}, fmt.Errorf("no retained result matches this selector on the active branch")
		}
		if len(matches) > 1 {
			ids := make([]string, 0, min(len(matches), 32))
			for _, match := range matches[max(0, len(matches)-32):] {
				ids = append(ids, match.ID)
			}
			return tool.StoredToolResult{}, fmt.Errorf("call_id matches %d results; select entry_id (up to 32 most recent): %s", len(matches), strings.Join(ids, ", "))
		}
		cloned, err := llm.CloneAgentMessage(matches[0].Message)
		if err != nil {
			return tool.StoredToolResult{}, fmt.Errorf("retained result is invalid")
		}
		matches[0].Message = cloned.(llm.ToolResultMessage)
		return matches[0], ctx.Err()
	})
	return context.WithValue(ctx, resultReaderContextKey{}, reader)
}
