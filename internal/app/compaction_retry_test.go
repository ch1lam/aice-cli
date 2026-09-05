package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestCompactionRetryUsesFinalSummaryAndAllAttemptUsage(t *testing.T) {
	t.Parallel()
	finalErr := &llm.ProviderError{StatusCode: 401, Err: errors.New("credential rejected")}
	for _, test := range []struct {
		name      string
		text      string
		err       error
		wantError string
	}{
		{name: "success", text: "final summary"},
		{name: "final failure", text: "unusable partial", err: finalErr, wantError: "credential rejected"},
		{name: "empty final summary", text: " \n ", wantError: "no visible text"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			path := filepath.Join(t.TempDir(), "session.jsonl")
			runPrintTurn(t, workspace, path, "first question", "first answer")
			runPrintTurn(t, workspace, path, "second question", "second answer")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			store, err := session.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			model := &retrySummaryModel{text: test.text, finalErr: test.err}
			application := &application{dependencies: dependencies{compactionKeepRecentTokens: 1}}
			configured := configuredModel{service: model, model: llm.Model{ID: "summary", API: "test-api", Provider: "test-provider", ContextWindow: 100000, MaxTokens: 1000}}
			_, err = application.compactSession(t.Context(), store, nil, &configured)
			if model.calls != 2 {
				t.Fatalf("model calls = %d, want actual retry", model.calls)
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				if test.err != nil && !errors.Is(err, test.err) {
					t.Fatalf("lost final error cause: %v", err)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(before) != string(after) {
					t.Fatal("failed summary changed source session")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Compactions) != 1 {
				t.Fatalf("checkpoints = %d, want one", len(snapshot.Compactions))
			}
			checkpoint := snapshot.Compactions[0]
			wantUsage := llm.Usage{InputTokens: 30, OutputTokens: 6, ReasoningTokens: 3, CacheReadTokens: 9, CacheWriteTokens: 12, TotalTokens: 36, Cost: &llm.Cost{Input: 3, Output: 6, CacheRead: 9, CacheWrite: 12, Total: 30}}
			if checkpoint.Summary != "final summary" || !reflect.DeepEqual(checkpoint.Usage, wantUsage) {
				t.Fatalf("checkpoint summary/usage = %q / %#v, want final text and both attempts", checkpoint.Summary, checkpoint.Usage)
			}
		})
	}
}

type retrySummaryModel struct {
	calls    int
	text     string
	finalErr error
}

func (m *retrySummaryModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.calls++
	message := llm.NewAssistantMessage(request.Model)
	message.StopReason = llm.StopReasonStop
	message.Content = []llm.ContentPart{llm.NewTextContent(m.text).Part()}
	factor := int64(m.calls)
	message.Usage = llm.Usage{InputTokens: 10 * factor, OutputTokens: 2 * factor, ReasoningTokens: factor, CacheReadTokens: 3 * factor, CacheWriteTokens: 4 * factor, TotalTokens: 12 * factor, Cost: &llm.Cost{Input: float64(factor), Output: float64(2 * factor), CacheRead: float64(3 * factor), CacheWrite: float64(4 * factor), Total: float64(10 * factor)}}
	err := m.finalErr
	if m.calls == 1 {
		err = &llm.ProviderError{StatusCode: 503, Err: errors.New("temporary failure")}
		message.Content = []llm.ContentPart{llm.NewTextContent("discard partial summary").Part()}
	}
	terminal := llm.Event{Type: llm.EventTypeDone, StopReason: llm.StopReasonStop, Message: &message}
	if err != nil {
		message.StopReason = llm.StopReasonError
		message.ErrorMessage = err.Error()
		terminal.Type = llm.EventTypeError
		terminal.StopReason = llm.StopReasonError
		terminal.Err = err
	}
	return &eventStream{events: []llm.Event{{Type: llm.EventTypeStart}, terminal}}, nil
}
