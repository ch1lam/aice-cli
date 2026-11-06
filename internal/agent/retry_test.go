package agent_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestLoopRetriesTransientModelFailureWithoutReplayingFailedHistory(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	providerErr := &llm.ProviderError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_error",
		Err:        errors.New("rate limited"),
	}
	failed := assistantMessage(modelInfo, llm.StopReasonError, textPart("partial"))
	failed.ErrorMessage = "rate limited"
	succeeded := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: []llm.Event{
			{Type: llm.EventTypeStart},
			{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "partial"},
			{Type: llm.EventTypeError, Message: &failed, Err: providerErr},
		}},
		{events: terminalEvents(succeeded)},
	}}
	loop := mustRetryLoop(t, model, 1, 0)

	var events []agent.AgentEvent
	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "hello")),
		collectEvents(&events),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	if len(model.requests[1].Messages) != 1 ||
		model.requests[1].Messages[0].MessageRole() != llm.RoleUser {
		t.Fatalf("retry request messages = %#v, want only original user prompt", model.requests[1].Messages)
	}
	if len(result.ModelRounds) != 2 ||
		result.ModelRounds[0].Assistant.StopReason != llm.StopReasonError ||
		result.ModelRounds[1].Assistant.StopReason != llm.StopReasonStop {
		t.Fatalf("Run() turns = %#v", result.ModelRounds)
	}
	workspace := t.TempDir()
	store, err := session.Create(t.Context(), filepath.Join(workspace, "session.jsonl"), session.Metadata{
		ID: "retry-session", CreatedAt: time.Now().UnixMilli(), WorkingDirectory: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parentID := ""
	for index, message := range result.Messages() {
		entry, err := session.NewMessage(strconv.Itoa(index), parentID, time.Now().UnixMilli(), message)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMessage(t.Context(), entry); err != nil {
			t.Fatalf("persist retry message %d: %v", index, err)
		}
		parentID = entry.ID
	}

	var retries []*agent.RetryEvent
	for _, event := range events {
		if event.Retry != nil {
			retries = append(retries, event.Retry)
		}
	}
	if len(retries) != 2 ||
		retries[0].Attempt != 1 || retries[0].Success ||
		retries[1].Attempt != 1 || !retries[1].Success {
		t.Fatalf("retry events = %#v", retries)
	}
}

func TestLoopRetriesProviderFailureBeforeStreamStarts(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	providerErr := &llm.ProviderError{
		StatusCode: http.StatusTooManyRequests,
		Err:        errors.New("rate limited before stream"),
	}
	succeeded := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &startFailureModel{
		startErr: providerErr,
		script:   &streamScript{events: terminalEvents(succeeded)},
	}
	loop := mustRetryLoop(t, model, 1, 0)

	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "hello")),
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if model.calls != 2 || len(model.requests[1].Messages) != 1 {
		t.Fatalf("model calls/requests = %d/%#v", model.calls, model.requests)
	}
	if len(result.ModelRounds) != 2 ||
		result.ModelRounds[0].Assistant.StopReason != llm.StopReasonError ||
		result.ModelRounds[1].Assistant.StopReason != llm.StopReasonStop {
		t.Fatalf("Run() turns = %#v", result.ModelRounds)
	}
	if got, want := result.ModelRounds[0].Assistant.ErrorMessage, "model request failed before completion: provider HTTP 429"; got != want {
		t.Fatalf("failed attempt error_message = %q, want %q", got, want)
	}
}

func TestLoopRecordsProviderSummaryWhenRequestNeverStarts(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	model := &startFailureModel{startErr: &llm.ProviderError{
		StatusCode: http.StatusUnauthorized,
		Code:       "authentication_error",
		Err:        errors.New("invalid x-api-key sk-secret"),
	}}
	loop := mustRetryLoop(t, model, 3, 0)

	result, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "hello")), nil)
	if err == nil {
		t.Fatal("Run() error = nil, want provider failure")
	}
	messages := result.Messages()
	last, ok := messages[len(messages)-1].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("last message = %#v, want assistant", messages[len(messages)-1])
	}
	if want := "agent run failed before completion: provider HTTP 401 authentication_error"; last.ErrorMessage != want {
		t.Fatalf("terminal error_message = %q, want %q", last.ErrorMessage, want)
	}
}

func TestLoopRecordsEveryAttemptWhenRetriesAreExhausted(t *testing.T) {
	t.Parallel()

	model := &startFailureModel{startErr: &llm.ProviderError{StatusCode: http.StatusServiceUnavailable}, failAll: true}
	loop := mustRetryLoop(t, model, 2, 0)

	var events []agent.AgentEvent
	result, err := loop.Run(t.Context(), testInput(testModel(), mustPrompt(t, "hello")), collectEvents(&events))
	if err == nil {
		t.Fatal("Run() error = nil, want provider failure")
	}
	var recorded []string
	for _, message := range result.Messages() {
		if assistant, ok := message.(llm.AssistantMessage); ok {
			recorded = append(recorded, assistant.ErrorMessage)
		}
	}
	want := "model request failed before completion: provider HTTP 503"
	if model.calls != 3 || !reflect.DeepEqual(recorded, []string{want, want, want}) {
		t.Fatalf("calls = %d, recorded = %q, want one record per attempt", model.calls, recorded)
	}
	var turnEnds int
	for _, event := range events {
		if event.Type == agent.EventTypeTurnEnd {
			turnEnds++
		}
	}
	if turnEnds != 3 {
		t.Fatalf("turn_end events = %d, want 3", turnEnds)
	}
}

func TestLoopDoesNotExecuteToolCallFromFailedRetryAttempt(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	providerErr := &llm.ProviderError{StatusCode: 503, Err: errors.New("unavailable")}
	failed := assistantMessage(
		modelInfo,
		llm.StopReasonError,
		toolCallPart("call-failed", "write", `{"path":"README.md"}`),
	)
	failed.ErrorMessage = "unavailable"
	succeeded := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: providerErr}}},
		{events: terminalEvents(succeeded)},
	}}
	tool := newFakeTool("write", nil)
	loop, err := agent.NewLoop(
		model,
		[]agent.Tool{tool},
		agent.WithGuard(allowAllGuard{}),
		agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 1, MaxDelay: time.Hour}),
	)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}

	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "write")),
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(tool.calls) != 0 {
		t.Fatalf("tool calls = %#v, want none", tool.calls)
	}
	if len(result.ModelRounds) != 2 ||
		len(result.ModelRounds[0].ToolResults) != 1 ||
		!result.ModelRounds[0].ToolResults[0].IsError {
		t.Fatalf("failed retry turn = %#v", result.ModelRounds[0])
	}
	if len(model.requests[1].Messages) != 1 ||
		model.requests[1].Messages[0].MessageRole() != llm.RoleUser {
		t.Fatalf("retry request messages = %#v", model.requests[1].Messages)
	}
}

func TestLoopDoesNotRetryPermanentProviderFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  *llm.ProviderError
	}{
		{
			name: "authentication",
			err:  &llm.ProviderError{StatusCode: 401, Err: errors.New("unauthorized")},
		},
		{
			name: "quota exhaustion",
			err: &llm.ProviderError{
				StatusCode: 429,
				Code:       "insufficient_quota",
				Err:        errors.New("quota exhausted"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			modelInfo := testModel()
			failed := assistantMessage(modelInfo, llm.StopReasonError)
			failed.ErrorMessage = test.err.Error()
			model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
				{Type: llm.EventTypeStart},
				{Type: llm.EventTypeError, Message: &failed, Err: test.err},
			}}}}
			loop := mustRetryLoop(t, model, 3, 0)

			var events []agent.AgentEvent
			_, err := loop.Run(
				t.Context(),
				testInput(modelInfo, mustPrompt(t, "hello")),
				collectEvents(&events),
			)
			if err == nil {
				t.Fatal("Run() error = nil, want provider failure")
			}
			if len(model.requests) != 1 {
				t.Fatalf("model requests = %d, want 1", len(model.requests))
			}
			for _, event := range events {
				if event.Retry != nil {
					t.Fatalf("unexpected retry event = %#v", event.Retry)
				}
			}
		})
	}
}

func TestLoopEmitsFailedRetryEndWhenAttemptsAreExhausted(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	providerErr := &llm.ProviderError{StatusCode: 503, Err: errors.New("unavailable")}
	failed := assistantMessage(modelInfo, llm.StopReasonError)
	failed.ErrorMessage = "unavailable"
	model := &scriptedModel{scripts: []*streamScript{
		{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: providerErr}}},
		{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: providerErr}}},
	}}
	loop := mustRetryLoop(t, model, 1, 0)

	var events []agent.AgentEvent
	_, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "hello")),
		collectEvents(&events),
	)
	if !errors.Is(err, providerErr) {
		t.Fatalf("Run() error = %v, want provider error", err)
	}
	var got []bool
	for _, event := range events {
		if event.Type == agent.EventTypeRetryEnd {
			got = append(got, event.Retry.Success)
		}
	}
	if !reflect.DeepEqual(got, []bool{false}) {
		t.Fatalf("retry_end success values = %v, want [false]", got)
	}
}

func TestLoopCancelsDuringRetryBackoff(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	providerErr := &llm.ProviderError{StatusCode: 503, Err: errors.New("unavailable")}
	failed := assistantMessage(modelInfo, llm.StopReasonError)
	failed.ErrorMessage = "unavailable"
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeError, Message: &failed, Err: providerErr},
	}}}}
	loop := mustRetryLoop(t, model, 1, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result, err := loop.Run(ctx, testInput(modelInfo, mustPrompt(t, "hello")), func(
		_ context.Context,
		event agent.AgentEvent,
	) error {
		if event.Type == agent.EventTypeRetryStart {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	if len(result.ModelRounds) != 2 ||
		result.ModelRounds[len(result.ModelRounds)-1].Assistant.StopReason != llm.StopReasonAborted {
		t.Fatalf("Run() turns = %#v, want aborted terminal turn", result.ModelRounds)
	}
}

func mustRetryLoop(
	t *testing.T,
	model agent.Model,
	maxRetries int,
	baseDelay time.Duration,
) *agent.Loop {
	t.Helper()

	loop, err := agent.NewLoop(model, nil, agent.WithRetryPolicy(agent.RetryPolicy{
		MaxRetries: maxRetries,
		BaseDelay:  baseDelay,
		MaxDelay:   time.Hour,
	}))
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	return loop
}

type startFailureModel struct {
	startErr error
	failAll  bool
	script   *streamScript
	calls    int
	requests []llm.Request
}

func (m *startFailureModel) Stream(
	_ context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.calls++
	m.requests = append(m.requests, request)
	if m.calls == 1 || m.failAll {
		return nil, m.startErr
	}
	return &scriptedStream{script: m.script}, nil
}
