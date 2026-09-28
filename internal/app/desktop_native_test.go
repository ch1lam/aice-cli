//go:build integration && (darwin || linux)

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

type nativePrintFixture struct {
	directory, name string
	pid             int
}
type nativePrintState struct {
	PID          int    `json:"pid"`
	Ticks        int    `json:"ticks"`
	Armed        bool   `json:"armed"`
	FrontIsLogin bool   `json:"front_is_login"`
	FrontPID     int    `json:"front_pid"`
	Active       bool   `json:"active"`
	FocusLosses  int    `json:"focus_losses"`
	KeysSent     int    `json:"keys_sent"`
	Commits      int    `json:"commits"`
	Value        string `json:"value"`
	Result       string `json:"result"`
}

func writeNativePrintSignal(t *testing.T, fixture nativePrintFixture, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.directory, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitNativePrintState(t *testing.T, ctx context.Context, fixture nativePrintFixture, check func(nativePrintState) bool) nativePrintState {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var last nativePrintState
	for {
		data, err := os.ReadFile(filepath.Join(fixture.directory, "state.json"))
		if err == nil {
			var state nativePrintState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			last = state
			if state.FrontIsLogin {
				t.Fatal("native fixture requires an available desktop; loginwindow is foreground (no unlock attempted)")
			}
			if check(state) {
				return state
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("synthetic fixture postcondition not reached: name=%s pid=%d active=%v armed=%v focus_losses=%d front_pid=%d ticks=%d", fixture.name, last.PID, last.Active, last.Armed, last.FocusLosses, last.FrontPID, last.Ticks)
		case <-tick.C:
		}
	}
}

func nativePrintValue(index int) string { return fmt.Sprintf("AICE CLI stage %d 中文 ✓", index+1) }

func verifyNativePrintSession(t *testing.T, ctx context.Context, path string, results []llm.ToolResultMessage, viewBudget int64) {
	t.Helper()
	store, err := session.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2*len(results)+2 || len(snapshot.Compactions) != 0 {
		t.Fatal("unexpected durable history shape")
	}
	var parent string
	seen := make(map[string]bool)
	pending := make(map[string]string)
	index, captures := 0, 0
	for _, entry := range snapshot.Messages {
		if entry.ID == "" || seen[entry.ID] || entry.ParentID != parent {
			t.Fatal("broken durable message identity or parent")
		}
		seen[entry.ID], parent = true, entry.ID
		switch message := entry.Message.(type) {
		case llm.AssistantMessage:
			for _, part := range message.Content {
				if part.ToolCall != nil {
					pending[part.ToolCall.ID] = part.ToolCall.Name
				}
			}
		case llm.ToolResultMessage:
			for _, part := range message.Content {
				if part.Image != nil {
					captures++
				}
			}
			if index >= len(results) || pending[message.ToolCallID] != message.ToolName {
				t.Fatalf("replayed tool identity differs at result %d", index)
			}
			view := llm.BoundToolResultView(message, viewBudget)
			if !reflect.DeepEqual(view, results[index]) {
				var savedJSON, deliveredJSON []byte
				if view.Details != nil {
					savedJSON = view.Details.StructuredContent
				}
				if results[index].Details != nil {
					deliveredJSON = results[index].Details.StructuredContent
				}
				t.Fatalf("replayed result %d differs: tool=%s content_equal=%t details_equal=%t structured_bytes=%d/%d structured_equal=%t", index, message.ToolName, reflect.DeepEqual(view.Content, results[index].Content), reflect.DeepEqual(view.Details, results[index].Details), len(savedJSON), len(deliveredJSON), bytes.Equal(savedJSON, deliveredJSON))
			}
			delete(pending, message.ToolCallID)
			index++
		}
	}
	if len(pending) != 0 || index != len(results) || captures != 9 || snapshot.LeafID != parent {
		t.Fatal("incomplete durable tool pairs")
	}
}
