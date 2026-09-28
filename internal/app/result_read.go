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

func withResultReader(ctx context.Context, store *session.Store, memory func() []llm.AgentMessage) context.Context {
	reader := resultReaderFunc(func(ctx context.Context, callID, entryID string) (tool.StoredToolResult, error) {
		if err := ctx.Err(); err != nil {
			return tool.StoredToolResult{}, err
		}
		var entries []session.MessageEntry
		if store != nil {
			snapshot, err := store.Snapshot()
			if err != nil {
				return tool.StoredToolResult{}, fmt.Errorf("Session result snapshot is unavailable")
			}
			branch, err := session.ActiveBranch(snapshot)
			if err != nil {
				return tool.StoredToolResult{}, fmt.Errorf("Session branch is unavailable")
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
