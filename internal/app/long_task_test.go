package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

const longTaskGoal = "LONGTASK_GOAL: inspect the fixture until the requested work is complete."
const longTaskSteer = "LONGTASK_CONSTRAINT: preserve the public API exactly."

// These exercise one Agent run, not 200 separate user interactions. Summary
// requests pass through the real application compactor and the same fake model.
func TestInteractiveSingleInputSurvives200ModelRounds(t *testing.T) {
	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "longtask.jsonl")
	model, deps := longTaskDependencies(t, workspace)
	deps.runTUI = func(ctx context.Context, runner tui.Runner, _ tui.Options) error {
		active, err := runner.NewRun(interaction.RunInput{Prompt: longTaskGoal}, nil)
		if err != nil {
			return err
		}
		model.onMain = func(round int) {
			if round == 100 {
				if err := active.Deliver(interaction.Delivery{
					ID: "mid-run-constraint", Text: longTaskSteer, Kind: interaction.DeliveryKindSteer,
				}); err != nil {
					t.Fatalf("Deliver() = %v", err)
				}
			}
		}
		model.requireSteerAfter = 100
		return active.Run(ctx)
	}
	command, err := newTestCommand(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--workspace", workspace, "--session", sessionPath})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	model.assertFinished(t)
	if model.summariesWithSteer < 2 {
		t.Fatalf("summaries carrying steering = %d, want >= 2", model.summariesWithSteer)
	}
	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Compactions) != model.summaryCalls {
		t.Fatalf("persisted checkpoints = %d, summary calls = %d", len(snapshot.Compactions), model.summaryCalls)
	}
	var users, assistants, results int
	for _, entry := range snapshot.Messages {
		switch message := entry.Message.(type) {
		case llm.UserMessage:
			users++
		case llm.AssistantMessage:
			assistants++
		case llm.ToolResultMessage:
			results++
			if message.IsError {
				t.Fatalf("read result %s failed", message.ToolCallID)
			}
		}
	}
	if users != 2 || assistants != 200 || results != 199 {
		t.Fatalf("source counts user/assistant/result = %d/%d/%d, want 2/200/199", users, assistants, results)
	}
	want := llm.AddUsage(model.mainUsage, model.summaryUsage)
	if got := session.TotalUsage(snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("durable total usage = %+v, want %+v", got, want)
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := llm.AgentMessagesToMessages(history)
	if err != nil {
		t.Fatal(err)
	}
	assertLongTaskPairs(t, restored)
	if !strings.Contains(longTaskRequestText(t, restored), longTaskSteer) {
		t.Fatal("restored context lost accepted steering")
	}
}

func TestStatelessPrintSingleInputSurvives200ModelRounds(t *testing.T) {
	workspace := t.TempDir()
	model, deps := longTaskDependencies(t, workspace)
	command, err := newTestCommand(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--workspace", workspace, "--print", longTaskGoal})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	model.assertFinished(t)
	if _, err := os.Stat(filepath.Join(workspace, ".aice", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("stateless Print created a Session directory, stat error = %v", err)
	}
}

func longTaskDependencies(t *testing.T, workspace string) (*longTaskModel, dependencies) {
	t.Helper()
	// Real read executions grow context without external processes or credentials.
	data := strings.Repeat(strings.Repeat("fixture ", 8)+"\n", 32)
	if err := os.WriteFile(filepath.Join(workspace, "long-context.txt"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	info := llm.Model{ID: "test-model", Name: "Test model", API: "test-api", Provider: "test-provider", ContextWindow: 8_000, MaxTokens: 1_000}
	model := &longTaskModel{t: t}
	cfg, home := compactPrintConfig(t, info)
	return model, dependencies{
		loadConfig:  func() (config.Config, error) { return cfg, nil },
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		providers:   []provider.Provider{&compactTestProvider{model: info, service: model}},
		userHomeDir: func() (string, error) { return home, nil },
		// Leave retention at the production default; the automatic window cap must
		// make progress even when the nominal 20k retention is too large.
	}
}

type longTaskModel struct {
	t                                           *testing.T
	mainCalls, summaryCalls, summariesWithSteer int
	mainUsage, summaryUsage                     llm.Usage
	onMain                                      func(int)
	requireSteerAfter                           int
}

func (m *longTaskModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	text := longTaskRequestText(m.t, request.Messages)
	response := llm.NewAssistantMessage(request.Model)
	response.StopReason = llm.StopReasonStop
	output := longTaskGoal
	if request.SystemPrompt == compactionSystemPrompt {
		m.summaryCalls++
		if tokens := llm.EstimateContextTokens(request).Tokens; tokens >= request.Model.ContextWindow {
			m.t.Fatalf("summary request exceeds window: %d", tokens)
		}
		// A summary only sees the selected prefix. It must copy a requirement once
		// that requirement enters the prefix; it cannot invent a retained-tail input.
		if !strings.Contains(text, longTaskGoal) {
			m.t.Fatal("summary input lost the original objective")
		}
		if strings.Contains(text, longTaskSteer) {
			output += "\n" + longTaskSteer
			m.summariesWithSteer++
		}
		response.Usage = llm.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, Cost: &llm.Cost{Total: 0.125}}
		m.summaryUsage = llm.AddUsage(m.summaryUsage, response.Usage)
	} else {
		m.mainCalls++
		if m.mainCalls > 200 {
			return nil, fmt.Errorf("unexpected main round %d", m.mainCalls)
		}
		assertLongTaskPairs(m.t, request.Messages)
		if !strings.Contains(text, longTaskGoal) {
			m.t.Fatalf("round %d lost original objective", m.mainCalls)
		}
		if m.requireSteerAfter > 0 && m.mainCalls > m.requireSteerAfter && !strings.Contains(text, longTaskSteer) {
			m.t.Fatalf("round %d lost accepted steering", m.mainCalls)
		}
		estimate := llm.EstimateContextTokens(request).Tokens
		reserve, safety := llm.ContextBudgets(request.Model.ContextWindow)
		if estimate >= request.Model.ContextWindow-safety {
			m.t.Fatalf("round %d over request budget: %d (reserve %d)", m.mainCalls, estimate, reserve)
		}
		response.Usage = llm.Usage{InputTokens: estimate, OutputTokens: 1, TotalTokens: estimate + 1, Cost: &llm.Cost{Total: 0.0625}}
		m.mainUsage = llm.AddUsage(m.mainUsage, response.Usage)
		if m.onMain != nil {
			m.onMain(m.mainCalls)
		}
		output = fmt.Sprintf("completed inspection round %d", m.mainCalls)
	}
	response.Content = []llm.ContentPart{llm.NewTextContent(output).Part()}
	events := []llm.Event{
		{Type: llm.EventTypeStart}, {Type: llm.EventTypeTextStart, ContentIndex: 0},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: output}, {Type: llm.EventTypeTextEnd, ContentIndex: 0},
	}
	if request.SystemPrompt != compactionSystemPrompt && m.mainCalls < 200 {
		call := llm.ToolCall{ID: fmt.Sprintf("read-%d", m.mainCalls), Name: "read", Arguments: json.RawMessage(`{"path":"long-context.txt"}`)}
		response.Content = append(response.Content, llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &call})
		response.StopReason = llm.StopReasonToolUse
		events = append(events, llm.Event{Type: llm.EventTypeToolCallStart, ContentIndex: 1}, llm.Event{Type: llm.EventTypeToolCallEnd, ContentIndex: 1, ToolCall: &call})
	}
	events = append(events, llm.Event{Type: llm.EventTypeDone, StopReason: response.StopReason, Message: &response})
	return &eventStream{events: events}, nil
}

func (m *longTaskModel) assertFinished(t *testing.T) {
	t.Helper()
	t.Logf("main requests=%d; summary requests=%d; summaries with steering=%d", m.mainCalls, m.summaryCalls, m.summariesWithSteer)
	if m.mainCalls != 200 || m.summaryCalls < 3 {
		t.Fatalf("main/summary requests = %d/%d, want 200/>=3", m.mainCalls, m.summaryCalls)
	}
}

func longTaskRequestText(t *testing.T, messages []llm.Message) string {
	t.Helper()
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertLongTaskPairs(t *testing.T, messages []llm.Message) {
	t.Helper()
	pending := map[string]bool{}
	for index, message := range messages {
		if result, ok := message.(llm.ToolResultMessage); ok {
			if !pending[result.ToolCallID] {
				t.Fatalf("message %d: orphan result %q", index, result.ToolCallID)
			}
			if result.IsError {
				t.Fatalf("message %d: read failed", index)
			}
			delete(pending, result.ToolCallID)
			continue
		}
		if len(pending) > 0 {
			t.Fatalf("message %d interrupts unfinished tool group", index)
		}
		if assistant, ok := message.(llm.AssistantMessage); ok {
			for _, part := range assistant.Content {
				if part.ToolCall != nil {
					if pending[part.ToolCall.ID] {
						t.Fatalf("duplicate call ID %q in one group", part.ToolCall.ID)
					}
					pending[part.ToolCall.ID] = true
				}
			}
		}
	}
	if len(pending) > 0 {
		t.Fatal("model request ends inside a tool group")
	}
}

func TestInteractiveCompactionFailureBoundaries(t *testing.T) {
	for _, boundary := range []string{"cancel-summary", "close-before-checkpoint", "cancel-after-checkpoint"} {
		t.Run(boundary, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(t.TempDir(), "fault.jsonl")
			_, deps := longTaskDependencies(t, workspace)
			cfg, err := deps.loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			info := deps.providers[0].DefaultModel()
			store := createAppTestSession(t, path, workspace)
			user, err := llm.NewUserMessage(llm.NewTextContent(longTaskGoal).Part())
			if err != nil {
				t.Fatal(err)
			}
			answer := llm.NewAssistantMessage(info)
			answer.Content = []llm.ContentPart{llm.NewTextContent(strings.Repeat("old source ", 3000)).Part()}
			answer.StopReason = llm.StopReasonStop
			if err := appendTestSessionMessages(t.Context(), store, []llm.AgentMessage{user, answer}); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			usage := llm.Usage{InputTokens: 13, OutputTokens: 5, TotalTokens: 18}
			model := &longTaskFaultModel{summary: controlledModel{response: longTaskGoal, stopReason: llm.StopReasonStop, usage: usage}}
			deps.newModel = func(config.Config) (agent.Model, error) { return model, nil }
			deps.providers = []provider.Provider{&compactTestProvider{model: info, service: model}}
			deps.loadConfig = func() (config.Config, error) { return cfg, nil }
			var beforeSummary []byte
			var runErr error
			deps.runTUI = func(ctx context.Context, runner tui.Runner, _ tui.Options) error {
				runCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				active, err := runner.NewRun(interaction.RunInput{Prompt: "continue"}, nil)
				if err != nil {
					return err
				}
				concrete, ok := runner.(*interactiveSession)
				if !ok {
					t.Fatalf("runner = %T, want application interactiveSession", runner)
				}
				model.onSummary = func() error {
					beforeSummary, err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					switch boundary {
					case "cancel-summary":
						cancel()
						return runCtx.Err()
					case "close-before-checkpoint":
						if err := concrete.conversation.store.Close(); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				}
				model.onMain = func() error { cancel(); return runCtx.Err() }
				runErr = active.Run(runCtx)
				return runErr
			}
			command, err := newTestCommand(t, deps)
			if err != nil {
				t.Fatal(err)
			}
			command.SetIn(strings.NewReader(""))
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs([]string{"--workspace", workspace, "--session", path})
			_ = command.ExecuteContext(t.Context()) // Closing an injected closed store can also report an error.
			wantErr := error(context.Canceled)
			if boundary == "close-before-checkpoint" {
				wantErr = session.ErrClosed
			}
			if !errors.Is(runErr, wantErr) {
				t.Fatalf("run error = %v, want %v", runErr, wantErr)
			}
			if model.summaryCalls != 1 {
				t.Fatalf("summary calls = %d, want 1", model.summaryCalls)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(beforeSummary) == 0 || !strings.HasPrefix(string(after), string(beforeSummary)) {
				t.Fatal("fault rewrote source bytes")
			}
			wantCheckpoints, wantMain := 0, 0
			wantUsage := llm.Usage{}
			if boundary == "cancel-after-checkpoint" {
				wantCheckpoints, wantMain, wantUsage = 1, 1, usage
			}
			if model.mainCalls != wantMain {
				t.Fatalf("main calls = %d, want %d", model.mainCalls, wantMain)
			}
			// Reopening twice checks that loading the persisted checkpoint leaves its
			// count and usage unchanged after the next main request was canceled.
			for reopen := 0; reopen < 2; reopen++ {
				snapshot := openSessionSnapshot(t, path)
				if _, err := session.BuildContext(snapshot); err != nil {
					t.Fatalf("reopen context: %v", err)
				}
				if len(snapshot.Compactions) != wantCheckpoints {
					t.Fatalf("checkpoints = %d, want %d", len(snapshot.Compactions), wantCheckpoints)
				}
				if got := session.TotalUsage(snapshot); !reflect.DeepEqual(got, wantUsage) {
					t.Fatalf("usage after reopen = %+v, want %+v", got, wantUsage)
				}
			}
		})
	}
}

type longTaskFaultModel struct {
	summary                 controlledModel
	summaryCalls, mainCalls int
	onSummary, onMain       func() error
}

func (m *longTaskFaultModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.SystemPrompt == compactionSystemPrompt {
		m.summaryCalls++
		if err := m.onSummary(); err != nil {
			return nil, err
		}
		return m.summary.Stream(ctx, request)
	}
	m.mainCalls++
	return nil, m.onMain()
}
