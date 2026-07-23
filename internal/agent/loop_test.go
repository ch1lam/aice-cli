package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestNewLoopRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{}
	validLimits := agent.Limits{MaxTurns: 2, MaxToolSteps: 2}
	validTool := newFakeTool("read", nil)

	tests := []struct {
		name    string
		model   agent.Model
		tools   []agent.Tool
		limits  agent.Limits
		wantErr string
	}{
		{
			name:    "missing model",
			limits:  validLimits,
			wantErr: "model is required",
		},
		{
			name:    "zero turns",
			model:   model,
			limits:  agent.Limits{MaxToolSteps: 1},
			wantErr: "max turns must be positive",
		},
		{
			name:    "zero tool steps",
			model:   model,
			limits:  agent.Limits{MaxTurns: 1},
			wantErr: "max tool steps must be positive",
		},
		{
			name:    "nil tool",
			model:   model,
			tools:   []agent.Tool{nil},
			limits:  validLimits,
			wantErr: "tool 0 is nil",
		},
		{
			name:    "duplicate tool",
			model:   model,
			tools:   []agent.Tool{validTool, newFakeTool("read", nil)},
			limits:  validLimits,
			wantErr: "duplicate tool name",
		},
		{
			name:  "invalid tool schema",
			model: model,
			tools: []agent.Tool{&fakeTool{definition: llm.ToolDefinition{
				Name:        "read",
				InputSchema: json.RawMessage(`[]`),
			}}},
			limits:  validLimits,
			wantErr: "input schema must be a json object",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.NewLoop(test.model, test.tools, test.limits)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("NewLoop() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestLoopRunTextTurn(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("hello"))
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "hello"},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{Type: llm.EventTypeUsage, Usage: &llm.Usage{OutputTokens: 1, TotalTokens: 1}},
		{Type: llm.EventTypeDone, StopReason: llm.StopReasonStop, Message: &answer},
	}}}}
	loop := mustLoop(t, model, nil, agent.Limits{MaxTurns: 3, MaxToolSteps: 3})
	prompt := mustPrompt(t, "hi")

	var events []agent.AgentEvent
	result, err := loop.Run(t.Context(), testInput(modelInfo, prompt), collectEvents(&events))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.Turns) != 1 || result.Turns[0].Assistant.Content[0].Text != "hello" {
		t.Fatalf("Run() result turns = %#v", result.Turns)
	}
	if got, want := messageRoles(result.Messages()), []llm.Role{llm.RoleUser, llm.RoleAssistant}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Result.Messages() roles = %v, want %v", got, want)
	}
	if len(model.requests) != 1 || len(model.requests[0].Messages) != 1 {
		t.Fatalf("model requests = %#v", model.requests)
	}
	if !model.scripts[0].closed {
		t.Fatal("model stream was not closed")
	}

	wantEventTypes := []agent.EventType{
		agent.EventTypeAgentStart,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageUpdate,
		agent.EventTypeMessageUpdate,
		agent.EventTypeMessageUpdate,
		agent.EventTypeMessageUpdate,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeAgentEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
	assertAgentMessage(t, events[2].Message, prompt)
	assertAgentMessage(t, events[3].Message, prompt)
	startedAssistant, ok := events[4].Message.(llm.AssistantMessage)
	if !ok || startedAssistant.Role != llm.RoleAssistant || len(startedAssistant.Content) != 0 {
		t.Errorf("assistant message_start = %#v", events[4].Message)
	}
	for _, event := range events[5:9] {
		if event.AssistantMessageEvent == nil {
			t.Errorf("message_update event = %#v, want assistant message event", event)
		}
	}
	assertAgentMessage(t, events[9].Message, answer)
	assertAgentMessage(t, events[10].Message, answer)
	if len(events[10].ToolResults) != 0 {
		t.Errorf("text turn tool results = %#v, want none", events[10].ToolResults)
	}
	if !reflect.DeepEqual(events[len(events)-1].Messages, result.Messages()) ||
		events[len(events)-1].Err != nil {
		t.Fatalf("agent_end event = %#v", events[len(events)-1])
	}
}

func TestLoopProjectsCompactionSummaryAtRequestBoundary(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{
		events: terminalEvents(answer),
	}}}
	loop := mustLoop(t, model, nil, agent.Limits{
		MaxTurns:     2,
		MaxToolSteps: 2,
	})
	input := testInput(modelInfo, mustPrompt(t, "continue"))
	input.History = []llm.AgentMessage{
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "Earlier work completed the provider adapter.",
			TokensBefore: 120_000,
			Timestamp:    1_721_234_567_893,
		},
	}

	if _, err := loop.Run(t.Context(), input, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 1 || len(model.requests[0].Messages) != 2 {
		t.Fatalf("model requests = %#v", model.requests)
	}
	projected, ok := model.requests[0].Messages[0].(llm.UserMessage)
	if !ok ||
		len(projected.Content) != 1 ||
		!strings.Contains(projected.Content[0].Text, "<summary>") {
		t.Fatalf("projected summary = %#v", model.requests[0].Messages[0])
	}
}

func TestLoopClampsMaxTokensToRemainingContext(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	modelInfo.ContextWindow = 100_000
	modelInfo.MaxTokens = 80_000
	answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{
		events: terminalEvents(answer),
	}}}
	loop := mustLoop(t, model, nil, agent.Limits{
		MaxTurns:     2,
		MaxToolSteps: 2,
	})
	previous := assistantMessage(
		modelInfo,
		llm.StopReasonStop,
		textPart("previous answer"),
	)
	previous.Usage = llm.Usage{TotalTokens: 60_000}
	input := testInput(modelInfo, mustPrompt(t, "continue"))
	input.Options.MaxTokens = modelInfo.MaxTokens
	input.History = []llm.AgentMessage{
		mustPrompt(t, "previous prompt"),
		previous,
	}

	if _, err := loop.Run(t.Context(), input, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	estimate := llm.EstimateContextTokens(request)
	if request.Options.MaxTokens <= 0 ||
		request.Options.MaxTokens >= modelInfo.MaxTokens {
		t.Fatalf(
			"request max tokens = %d, want a positive clamped value below %d",
			request.Options.MaxTokens,
			modelInfo.MaxTokens,
		)
	}
	if got, want := request.Options.MaxTokens+estimate.Tokens, modelInfo.ContextWindow-4_096; got != want {
		t.Fatalf("context plus output tokens = %d, want %d", got, want)
	}
}

func TestLoopRejectsContextAboveCompactionThreshold(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	modelInfo.ContextWindow = 100_000
	modelInfo.MaxTokens = 20_000
	model := &scriptedModel{}
	loop := mustLoop(t, model, nil, agent.Limits{
		MaxTurns:     2,
		MaxToolSteps: 2,
	})
	previous := assistantMessage(
		modelInfo,
		llm.StopReasonStop,
		textPart("previous answer"),
	)
	previous.Usage = llm.Usage{TotalTokens: 90_000}
	input := testInput(modelInfo, mustPrompt(t, "continue"))
	input.History = []llm.AgentMessage{
		mustPrompt(t, "previous prompt"),
		previous,
	}

	_, err := loop.Run(t.Context(), input, nil)
	if !errors.Is(err, agent.ErrContextLimit) {
		t.Fatalf("Run() error = %v, want ErrContextLimit", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("model requests = %d, want context rejection before provider", len(model.requests))
	}
}

func TestLoopSettlesToolRunAfterCrossingCompactionThreshold(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	modelInfo.ContextWindow = 100_000
	modelInfo.MaxTokens = 20_000
	first := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("call-1", "read", `{"path":"a.go"}`),
	)
	first.Usage = llm.Usage{TotalTokens: 90_000}
	second := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(second)},
	}}
	loop := mustLoop(
		t,
		model,
		[]agent.Tool{newFakeTool("read", nil)},
		agent.Limits{MaxTurns: 2, MaxToolSteps: 2},
	)

	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "inspect")),
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Turns) != 2 || len(model.requests) != 2 {
		t.Fatalf(
			"result turns = %d, model requests = %d, want settled two-turn run",
			len(result.Turns),
			len(model.requests),
		)
	}
	if got := model.requests[1].Options.MaxTokens; got <= 0 || got >= modelInfo.MaxTokens {
		t.Fatalf(
			"second request max tokens = %d, want positive clamped continuation",
			got,
		)
	}
}

func TestLoopRunToolTurnThenContinues(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	first := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("call-1", "read", `{"path":"a.go"}`),
		toolCallPart("call-2", "read", `{"path":"b.go"}`),
	)
	first.ResponseModelID = "resolved-model"
	first.ResponseID = "response-1"
	first.Usage = llm.Usage{
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
		Cost:         &llm.Cost{Input: 0.001, Output: 0.002, Total: 0.003},
	}
	first.ErrorMessage = "redacted provider diagnostic"
	first.Timestamp = 1_721_234_567_890
	second := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(second)},
	}}
	tool := newFakeTool("read", func(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{
			CallID:  "wrong-call-id",
			Name:    "wrong-name",
			Content: []llm.ContentPart{textPart("output:" + call.ID)},
		}, nil
	})
	loop := mustLoop(t, model, []agent.Tool{tool}, agent.Limits{MaxTurns: 3, MaxToolSteps: 3})

	var events []agent.AgentEvent
	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "inspect files")),
		collectEvents(&events),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := callIDs(tool.calls), []string{"call-1", "call-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call order = %v, want %v", got, want)
	}
	if len(result.Turns) != 2 || len(result.Turns[0].ToolResults) != 2 {
		t.Fatalf("Run() result = %#v", result)
	}
	for index, toolResult := range result.Turns[0].ToolResults {
		wantID := "call-" + string(rune('1'+index))
		if toolResult.ToolCallID != wantID || toolResult.ToolName != "read" || toolResult.IsError {
			t.Fatalf("tool result %d = %#v", index, toolResult)
		}
	}
	if got, want := messageRoles(result.Messages()), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleToolResult,
		llm.RoleAssistant,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Result.Messages() roles = %v, want %v", got, want)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	if got, want := messageRoles(model.requests[1].Messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleToolResult,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second request roles = %v, want %v", got, want)
	}
	replayedAssistant, ok := model.requests[1].Messages[1].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("second request assistant type = %T, want llm.AssistantMessage", model.requests[1].Messages[1])
	}
	if !reflect.DeepEqual(replayedAssistant, first) {
		t.Errorf("second request assistant = %#v, want %#v", replayedAssistant, first)
	}
	if len(model.requests[1].Tools) != 1 || model.requests[1].Tools[0].Name != "read" {
		t.Fatalf("second request tools = %#v", model.requests[1].Tools)
	}

	var executionIDs []string
	for _, event := range events {
		if event.Type == agent.EventTypeToolExecutionStart {
			executionIDs = append(executionIDs, event.ToolCall.ID)
		}
	}
	if want := []string{"call-1", "call-2"}; !reflect.DeepEqual(executionIDs, want) {
		t.Fatalf("tool execution event order = %v, want %v", executionIDs, want)
	}
	wantEventTypes := []agent.EventType{
		agent.EventTypeAgentStart,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeToolExecutionStart,
		agent.EventTypeToolExecutionEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeToolExecutionStart,
		agent.EventTypeToolExecutionEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeAgentEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
	assertAgentMessage(t, events[5].Message, first)
	assertAgentMessage(t, events[8].Message, result.Turns[0].ToolResults[0])
	assertAgentMessage(t, events[9].Message, result.Turns[0].ToolResults[0])
	assertAgentMessage(t, events[12].Message, result.Turns[0].ToolResults[1])
	assertAgentMessage(t, events[13].Message, result.Turns[0].ToolResults[1])
	assertAgentMessage(t, events[14].Message, first)
	if !reflect.DeepEqual(events[14].ToolResults, result.Turns[0].ToolResults) {
		t.Errorf(
			"first turn_end tool results = %#v, want %#v",
			events[14].ToolResults,
			result.Turns[0].ToolResults,
		)
	}
	assertAgentMessage(t, events[17].Message, second)
	assertAgentMessage(t, events[18].Message, second)
	if !reflect.DeepEqual(events[19].Messages, result.Messages()) {
		t.Errorf("agent_end messages = %#v, want %#v", events[19].Messages, result.Messages())
	}
}

func TestLoopDoesNotExecuteLengthTruncatedToolCalls(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	truncated := assistantMessage(
		modelInfo,
		llm.StopReasonLength,
		toolCallPart("call-1", "read", `{"path":"a.go"}`),
		toolCallPart("call-2", "missing", `{"path":"b.go"}`),
	)
	recovered := assistantMessage(modelInfo, llm.StopReasonStop, textPart("recovered"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(truncated)},
		{events: terminalEvents(recovered)},
	}}
	read := newFakeTool("read", successfulTool)
	loop := mustLoop(t, model, []agent.Tool{read}, agent.Limits{
		MaxTurns:     3,
		MaxToolSteps: 3,
	})

	var events []agent.AgentEvent
	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "inspect files")),
		collectEvents(&events),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(read.calls) != 0 {
		t.Fatalf("truncated tool execution count = %d, want 0", len(read.calls))
	}
	if len(result.Turns) != 2 || len(result.Turns[0].ToolResults) != 2 {
		t.Fatalf("Run() result = %#v", result)
	}
	wantCalls := []struct {
		id   string
		name string
	}{
		{id: "call-1", name: "read"},
		{id: "call-2", name: "missing"},
	}
	for index, want := range wantCalls {
		toolResult := result.Turns[0].ToolResults[index]
		if toolResult.ToolCallID != want.id ||
			toolResult.ToolName != want.name ||
			!toolResult.IsError {
			t.Errorf("truncated tool result %d = %#v, want error for %#v", index, toolResult, want)
		}
		if len(toolResult.Content) != 1 ||
			!strings.Contains(toolResult.Content[0].Text, "output token limit") {
			t.Errorf("truncated tool result %d content = %#v", index, toolResult.Content)
		}
	}

	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	if got, want := messageRoles(model.requests[1].Messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleToolResult,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second request roles = %v, want %v", got, want)
	}
	assertAgentMessage(t, model.requests[1].Messages[1], truncated)
	for index := range result.Turns[0].ToolResults {
		assertAgentMessage(
			t,
			model.requests[1].Messages[index+2],
			result.Turns[0].ToolResults[index],
		)
	}

	wantEventTypes := []agent.EventType{
		agent.EventTypeAgentStart,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeToolExecutionStart,
		agent.EventTypeToolExecutionEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeToolExecutionStart,
		agent.EventTypeToolExecutionEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeAgentEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
	for index, eventIndex := range []int{7, 11} {
		event := events[eventIndex]
		if event.ToolCall == nil ||
			event.ToolCall.ID != wantCalls[index].id ||
			event.ToolResult == nil ||
			!event.ToolResult.IsError ||
			event.Err == nil {
			t.Errorf("truncated tool_execution_end %d = %#v", index, event)
		}
	}
	if !reflect.DeepEqual(events[14].ToolResults, result.Turns[0].ToolResults) {
		t.Errorf(
			"truncated turn_end tool results = %#v, want %#v",
			events[14].ToolResults,
			result.Turns[0].ToolResults,
		)
	}
	if !reflect.DeepEqual(events[19].Messages, result.Messages()) {
		t.Errorf("agent_end messages = %#v, want %#v", events[19].Messages, result.Messages())
	}
}

func TestLoopRunWithWorkspaceReadTool(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "answer.txt"), []byte("from workspace\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	workspace, err := tool.NewWorkspace(workspacePath)
	if err != nil {
		t.Fatalf("tool.NewWorkspace() error = %v", err)
	}
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("tool.NewRead() error = %v", err)
	}

	modelInfo := testModel()
	first := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("read-1", "read", `{"path":"answer.txt"}`),
	)
	second := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(second)},
	}}
	loop := mustLoop(t, model, []agent.Tool{read}, agent.Limits{MaxTurns: 3, MaxToolSteps: 3})

	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "read the answer")),
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Turns) != 2 || len(result.Turns[0].ToolResults) != 1 {
		t.Fatalf("Run() result = %#v", result)
	}
	content := result.Turns[0].ToolResults[0].Content
	if len(content) != 1 || content[0].Text != "from workspace\n" {
		t.Fatalf("read tool result content = %#v", content)
	}
}

func TestLoopReturnsToolFailuresToModel(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	first := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("missing-1", "missing", `{}`),
		toolCallPart("fail-1", "read", `{}`),
	)
	second := assistantMessage(modelInfo, llm.StopReasonStop, textPart("recovered"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(second)},
	}}
	toolFailure := errors.New("disk unavailable")
	tool := newFakeTool("read", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{}, toolFailure
	})
	loop := mustLoop(t, model, []agent.Tool{tool}, agent.Limits{MaxTurns: 3, MaxToolSteps: 3})

	result, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "read")), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Turns) != 2 || len(result.Turns[0].ToolResults) != 2 {
		t.Fatalf("Run() result = %#v", result)
	}
	for index, toolResult := range result.Turns[0].ToolResults {
		if !toolResult.IsError {
			t.Fatalf("tool result %d IsError = false", index)
		}
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	for index := 2; index < 4; index++ {
		toolResult, ok := model.requests[1].Messages[index].(llm.ToolResultMessage)
		if !ok || !toolResult.IsError {
			t.Fatalf("second request message %d = %#v", index, model.requests[1].Messages[index])
		}
	}
}

func TestLoopEnforcesTurnLimitAfterCompletingToolResults(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	answer := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("call-1", "read", `{}`),
	)
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(answer)}}}
	tool := newFakeTool("read", successfulTool)
	loop := mustLoop(t, model, []agent.Tool{tool}, agent.Limits{MaxTurns: 1, MaxToolSteps: 2})

	result, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "read")), nil)
	if !errors.Is(err, agent.ErrTurnLimit) {
		t.Fatalf("Run() error = %v, want ErrTurnLimit", err)
	}
	if len(model.requests) != 1 || len(result.Turns) != 1 || len(result.Turns[0].ToolResults) != 1 {
		t.Fatalf("Run() requests = %d, result = %#v", len(model.requests), result)
	}
}

func TestLoopEnforcesToolStepLimitWithPairedResults(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	answer := assistantMessage(
		modelInfo,
		llm.StopReasonToolUse,
		toolCallPart("call-1", "read", `{}`),
		toolCallPart("call-2", "read", `{}`),
	)
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(answer)}}}
	tool := newFakeTool("read", successfulTool)
	loop := mustLoop(t, model, []agent.Tool{tool}, agent.Limits{MaxTurns: 2, MaxToolSteps: 1})

	result, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "read twice")), nil)
	if !errors.Is(err, agent.ErrToolStepLimit) {
		t.Fatalf("Run() error = %v, want ErrToolStepLimit", err)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(tool.calls))
	}
	if len(result.Turns) != 1 || len(result.Turns[0].ToolResults) != 2 {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.Turns[0].ToolResults[0].IsError || !result.Turns[0].ToolResults[1].IsError {
		t.Fatalf("tool results = %#v", result.Turns[0].ToolResults)
	}
	if got := result.Turns[0].ToolResults[1].ToolCallID; got != "call-2" {
		t.Fatalf("synthetic tool result call id = %q, want call-2", got)
	}
}

func TestLoopPreservesPartialAssistantOnCancellation(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	partial := assistantMessage(modelInfo, llm.StopReasonAborted, textPart("partial"))
	partial.ErrorMessage = context.Canceled.Error()
	ctx, cancel := context.WithCancel(t.Context())
	model := &scriptedModel{scripts: []*streamScript{{
		events: []llm.Event{
			{Type: llm.EventTypeStart},
			{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "partial"},
			{Type: llm.EventTypeError, StopReason: llm.StopReasonAborted, Message: &partial, Err: context.Canceled},
		},
		onNext: func(index int) {
			if index == 2 {
				cancel()
			}
		},
	}}}
	loop := mustLoop(t, model, nil, agent.Limits{MaxTurns: 2, MaxToolSteps: 2})

	var events []agent.AgentEvent
	result, err := loop.Run(ctx, testInput(modelInfo, mustPrompt(t, "work")), collectEvents(&events))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(result.Turns) != 1 || result.Turns[0].Assistant.Content[0].Text != "partial" {
		t.Fatalf("Run() result = %#v", result)
	}
	if events[len(events)-1].Type != agent.EventTypeAgentEnd ||
		!errors.Is(events[len(events)-1].Err, context.Canceled) {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
	wantEventTypes := []agent.EventType{
		agent.EventTypeAgentStart,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageUpdate,
		agent.EventTypeMessageEnd,
		agent.EventTypeTurnEnd,
		agent.EventTypeAgentEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
	assertAgentMessage(t, events[6].Message, partial)
	assertAgentMessage(t, events[7].Message, partial)
	if !reflect.DeepEqual(events[8].Messages, result.Messages()) {
		t.Errorf("agent_end messages = %#v, want %#v", events[8].Messages, result.Messages())
	}
}

func TestLoopRejectsStreamEOFBeforeTerminalEvent(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
	}}}}
	loop := mustLoop(t, model, nil, agent.Limits{MaxTurns: 2, MaxToolSteps: 2})

	var events []agent.AgentEvent
	result, err := loop.Run(
		t.Context(),
		testInput(modelInfo, mustPrompt(t, "work")),
		collectEvents(&events),
	)
	if !errors.Is(err, agent.ErrProtocol) {
		t.Fatalf("Run() error = %v, want ErrProtocol", err)
	}
	if len(result.Turns) != 0 {
		t.Fatalf("Run() result turns = %#v, want none", result.Turns)
	}
	if !model.scripts[0].closed {
		t.Fatal("model stream was not closed")
	}
	wantEventTypes := []agent.EventType{
		agent.EventTypeAgentStart,
		agent.EventTypeTurnStart,
		agent.EventTypeMessageStart,
		agent.EventTypeMessageEnd,
		agent.EventTypeMessageStart,
		agent.EventTypeTurnEnd,
		agent.EventTypeAgentEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
	if events[5].Message != nil {
		t.Errorf("failed turn_end message = %#v, want nil", events[5].Message)
	}
	if !reflect.DeepEqual(events[6].Messages, result.Messages()) {
		t.Errorf("agent_end messages = %#v, want %#v", events[6].Messages, result.Messages())
	}
}

func TestLoopRejectsUnknownStreamEvent(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeUnknown},
	}}}}
	loop := mustLoop(t, model, nil, agent.Limits{MaxTurns: 2, MaxToolSteps: 2})

	_, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "work")), nil)
	if !errors.Is(err, agent.ErrProtocol) {
		t.Fatalf("Run() error = %v, want ErrProtocol", err)
	}
	if !model.scripts[0].closed {
		t.Fatal("model stream was not closed")
	}
}

func TestLoopStopsImmediatelyWhenEventSinkFails(t *testing.T) {
	t.Parallel()

	modelInfo := testModel()
	answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "done"},
		{Type: llm.EventTypeDone, Message: &answer},
	}}}}
	loop := mustLoop(t, model, nil, agent.Limits{MaxTurns: 2, MaxToolSteps: 2})
	sinkFailure := errors.New("consumer stopped")
	var events []agent.AgentEvent

	_, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "work")), func(
		_ context.Context,
		event agent.AgentEvent,
	) error {
		events = append(events, event)
		if event.Type == agent.EventTypeMessageUpdate {
			return sinkFailure
		}
		return nil
	})
	if !errors.Is(err, sinkFailure) {
		t.Fatalf("Run() error = %v, want sink failure", err)
	}
	if !model.scripts[0].closed {
		t.Fatal("model stream was not closed")
	}
	for _, event := range events {
		if event.Type == agent.EventTypeAgentEnd {
			t.Fatal("agent_end emitted after event sink failure")
		}
	}
}

func testModel() llm.Model {
	return llm.Model{
		ID:        "test-model",
		API:       llm.API("test-api"),
		Provider:  llm.ProviderID("test-provider"),
		MaxTokens: 1024,
	}
}

func testInput(model llm.Model, prompt llm.UserMessage) agent.RunInput {
	return agent.RunInput{
		Model:        model,
		SystemPrompt: "You are a coding agent.",
		Prompt:       prompt,
		Options:      llm.StreamOptions{MaxTokens: 256},
	}
}

func mustPrompt(t *testing.T, text string) llm.UserMessage {
	t.Helper()

	prompt, err := llm.NewUserMessage(textPart(text))
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	return prompt
}

func mustLoop(
	t *testing.T,
	model agent.Model,
	tools []agent.Tool,
	limits agent.Limits,
) *agent.Loop {
	t.Helper()

	loop, err := agent.NewLoop(model, tools, limits)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	return loop
}

func textPart(text string) llm.ContentPart {
	return llm.NewTextContent(text).Part()
}

func toolCallPart(id, name, arguments string) llm.ContentPart {
	return llm.ContentPart{
		Type: llm.ContentTypeToolCall,
		ToolCall: &llm.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: json.RawMessage(arguments),
		},
	}
}

func assistantMessage(
	model llm.Model,
	stopReason llm.StopReason,
	content ...llm.ContentPart,
) llm.AssistantMessage {
	message := llm.NewAssistantMessage(model)
	message.Content = content
	message.StopReason = stopReason
	return message
}

func terminalEvents(message llm.AssistantMessage) []llm.Event {
	return []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message},
	}
}

func collectEvents(events *[]agent.AgentEvent) agent.AgentEventSink {
	return func(_ context.Context, event agent.AgentEvent) error {
		*events = append(*events, event)
		return nil
	}
}

func eventTypes(events []agent.AgentEvent) []agent.EventType {
	types := make([]agent.EventType, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return types
}

func assertAgentMessage(t *testing.T, got, want llm.AgentMessage) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agent event message = %#v, want %#v", got, want)
	}
}

func messageRoles[T llm.AgentMessage](messages []T) []llm.Role {
	roles := make([]llm.Role, len(messages))
	for index, message := range messages {
		switch value := any(message).(type) {
		case llm.UserMessage:
			roles[index] = value.Role
		case llm.AssistantMessage:
			roles[index] = value.Role
		case llm.ToolResultMessage:
			roles[index] = value.Role
		case llm.CompactionSummaryMessage:
			roles[index] = value.Role
		default:
			roles[index] = llm.RoleUnknown
		}
	}
	return roles
}

func callIDs(calls []llm.ToolCall) []string {
	ids := make([]string, len(calls))
	for index, call := range calls {
		ids[index] = call.ID
	}
	return ids
}

type streamScript struct {
	events   []llm.Event
	nextErr  error
	closeErr error
	onNext   func(index int)
	closed   bool
}

type scriptedStream struct {
	script          *streamScript
	index           int
	nextErrReturned bool
}

func (s *scriptedStream) Next() (llm.Event, error) {
	if s.index < len(s.script.events) {
		index := s.index
		s.index++
		if s.script.onNext != nil {
			s.script.onNext(index)
		}
		return s.script.events[index], nil
	}
	if s.script.nextErr != nil && !s.nextErrReturned {
		s.nextErrReturned = true
		return llm.Event{}, s.script.nextErr
	}
	return llm.Event{}, io.EOF
}

func (s *scriptedStream) Close() error {
	s.script.closed = true
	return s.script.closeErr
}

type scriptedModel struct {
	scripts  []*streamScript
	requests []llm.Request
}

func (m *scriptedModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	index := len(m.requests)
	m.requests = append(m.requests, request)
	if index >= len(m.scripts) {
		return nil, errors.New("unexpected model request")
	}
	return &scriptedStream{script: m.scripts[index]}, nil
}

type fakeTool struct {
	definition llm.ToolDefinition
	execute    func(context.Context, llm.ToolCall) (llm.ToolResult, error)
	calls      []llm.ToolCall
}

func newFakeTool(
	name string,
	execute func(context.Context, llm.ToolCall) (llm.ToolResult, error),
) *fakeTool {
	return &fakeTool{
		definition: llm.ToolDefinition{
			Name:        name,
			Description: "test tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		execute: execute,
	}
}

func (t *fakeTool) Definition() llm.ToolDefinition {
	return t.definition
}

func (t *fakeTool) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	t.calls = append(t.calls, call)
	if t.execute == nil {
		return successfulTool(ctx, call)
	}
	return t.execute(ctx, call)
}

func successfulTool(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	return llm.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: []llm.ContentPart{textPart("ok")},
	}, nil
}
