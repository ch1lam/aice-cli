package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestPrintTotalsIncludeSummaryRetriesAndFailures(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"text", "json"} {
		for _, persistent := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/session=%t/failure=%t", format, persistent, fail), func(t *testing.T) {
					t.Parallel()
					checkPrintSummaryUsage(t, format, persistent, fail)
				})
			}
		}
	}
}

func checkPrintSummaryUsage(t *testing.T, format string, persistent, fail bool) {
	t.Helper()
	workspace := t.TempDir()
	writeAppFile(t, workspace, "large.txt", strings.Repeat("x", 14000))
	path := filepath.Join(t.TempDir(), "session.jsonl")
	info := llm.Model{ID: "test", Name: "Test", API: "test-api", Provider: "test-provider", ContextWindow: 10000, MaxTokens: 1000}
	summary := &retrySummaryModel{text: "private compaction summary"}
	if fail {
		summary.finalErr = &llm.ProviderError{StatusCode: 401, Err: errors.New("summary denied")}
	}
	model := &compactionUsageModel{summary: summary}
	configuration, home := compactPrintConfig(t, info)
	command, err := newTestCommand(t, dependencies{
		loadConfig:  func() (config.Config, error) { return configuration, nil },
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		providers:   []provider.Provider{&compactTestProvider{model: info, service: model}},
		userHomeDir: func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	args := []string{"--workspace", workspace, "--print", "inspect", "--yolo", "--output-format", format}
	if persistent {
		args = append(args, "--session", path)
	}
	command.SetArgs(args)
	err = command.ExecuteContext(t.Context())
	if fail {
		if !errors.Is(err, summary.finalErr) {
			t.Fatalf("error=%v, want summary cause", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if summary.calls != 2 {
		t.Fatalf("summary attempts=%d, want retry", summary.calls)
	}
	wantTotal := int64(9043)
	if fail {
		wantTotal = 9036
	}
	if format == "text" {
		if !strings.Contains(diagnostics.String(), fmt.Sprintf("aice: total input_tokens=30 output_tokens=6 reasoning_tokens=3 cache_read_tokens=9 cache_write_tokens=12 total_tokens=%d cost_usd=30.000000", wantTotal)) {
			t.Fatalf("missing combined total: %s", diagnostics.String())
		}
		if strings.Contains(output.String(), "private compaction summary") {
			t.Fatal("summary leaked as assistant output")
		}
	} else {
		decoder := json.NewDecoder(&output)
		ends, messages := 0, 0
		for {
			var event struct {
				Type  string    `json:"type"`
				Text  string    `json:"text"`
				Usage llm.Usage `json:"usage"`
			}
			if err := decoder.Decode(&event); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatal(err)
			}
			if event.Type == "message_end" {
				messages++
				if event.Text == "private compaction summary" || event.Usage.TotalTokens == 12 || event.Usage.TotalTokens == 24 {
					t.Fatal("fake summary assistant event")
				}
			}
			if event.Type == "agent_end" {
				ends++
				if event.Usage.TotalTokens != wantTotal || event.Usage.InputTokens != 30 || event.Usage.OutputTokens != 6 || event.Usage.CacheReadTokens != 9 || event.Usage.CacheWriteTokens != 12 || event.Usage.ReasoningTokens != 3 || event.Usage.Cost == nil || event.Usage.Cost.Total != 30 {
					t.Fatalf("agent_end usage=%#v, want total %d", event.Usage, wantTotal)
				}
			}
		}
		wantMessages := 2
		if fail {
			wantMessages = 1 // Compaction failed before another main response.
		}
		if ends != 1 || messages != wantMessages {
			t.Fatalf("agent_end/message_end=%d/%d", ends, messages)
		}
	}
	if persistent {
		snapshot := openSessionSnapshot(t, path)
		wantCheckpoints := 1
		if fail {
			wantCheckpoints = 0
		}
		if len(snapshot.Compactions) != wantCheckpoints {
			t.Fatalf("checkpoints=%d", len(snapshot.Compactions))
		}
		if !fail && snapshot.Compactions[0].Usage.TotalTokens != 36 {
			t.Fatal("checkpoint double counted or missed summary retry")
		}
		if fail && session.TotalUsage(snapshot).TotalTokens != 9000 {
			t.Fatal("failed checkpoint unexpectedly became durable usage")
		}
	}
}

func TestCompactionReportsKnownUsageBeforeCheckpointWriteFailure(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	runPrintTurn(t, workspace, path, "first", "answer")
	runPrintTurn(t, workspace, path, "second", "answer")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	model := &compactionUsageModel{beforeSummary: func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}}
	configuration := configuredModel{service: model, model: llm.Model{ID: "test", API: "test-api", Provider: "test-provider", ContextWindow: 10000, MaxTokens: 1000}}
	application := &application{}
	calls := 0
	var known llm.Usage
	_, _, err = application.compactStoredHistory(t.Context(), store, &configuration, session.CompactionSettings{KeepRecentTokens: 1}, func(usage llm.Usage) { calls++; known = usage })
	if err == nil || !strings.Contains(err.Error(), "append session compaction") {
		t.Fatalf("error=%v, want checkpoint write failure", err)
	}
	if calls != 1 || known.TotalTokens != 23 {
		t.Fatalf("usage callbacks=%d usage=%#v", calls, known)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed checkpoint modified Session")
	}
}

type compactionUsageModel struct {
	summary       *retrySummaryModel
	beforeSummary func()
	mainCalls     int
}

func (m *compactionUsageModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.SystemPrompt == compactionSystemPrompt {
		if m.beforeSummary != nil {
			m.beforeSummary()
		}
		if m.summary != nil {
			return m.summary.Stream(ctx, request)
		}
		model := &controlledModel{response: "summary", stopReason: llm.StopReasonStop, usage: llm.Usage{TotalTokens: 23}}
		return model.Stream(ctx, request)
	}
	m.mainCalls++
	if m.mainCalls == 1 {
		message := llm.NewAssistantMessage(request.Model)
		message.StopReason = llm.StopReasonToolUse
		message.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "read", Name: "read", Arguments: []byte(`{"path":"large.txt"}`)}}}
		message.Usage = llm.Usage{TotalTokens: 9000}
		return &eventStream{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message}}}, nil
	}
	model := &controlledModel{response: "done", stopReason: llm.StopReasonStop, usage: llm.Usage{TotalTokens: 7}}
	return model.Stream(ctx, request)
}
