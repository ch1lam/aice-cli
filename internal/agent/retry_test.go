package agent_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
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
	if len(result.Turns) != 2 ||
		result.Turns[0].Assistant.StopReason != llm.StopReasonError ||
		result.Turns[1].Assistant.StopReason != llm.StopReasonStop {
		t.Fatalf("Run() turns = %#v", result.Turns)
	}
	if _, err := session.NewTurn(
		strings.Repeat("a", 32),
		"",
		time.Now().UnixMilli(),
		result.Messages(),
	); err != nil {
		t.Fatalf("session.NewTurn() error = %v", err)
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
	if len(result.Turns) != 2 ||
		result.Turns[0].Assistant.StopReason != llm.StopReasonError ||
		result.Turns[1].Assistant.StopReason != llm.StopReasonStop {
		t.Fatalf("Run() turns = %#v", result.Turns)
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
	if len(result.Turns) != 2 ||
		len(result.Turns[0].ToolResults) != 1 ||
		!result.Turns[0].ToolResults[0].IsError {
		t.Fatalf("failed retry turn = %#v", result.Turns[0])
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
	if len(result.Turns) != 2 ||
		result.Turns[len(result.Turns)-1].Assistant.StopReason != llm.StopReasonAborted {
		t.Fatalf("Run() turns = %#v, want aborted terminal turn", result.Turns)
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
	if m.calls == 1 {
		return nil, m.startErr
	}
	return &scriptedStream{script: m.script}, nil
}
