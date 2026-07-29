package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Loop runs provider-neutral model and tool turns. It is safe to reuse as long
// as the supplied Model and Tools are safe for concurrent use.
type Loop struct {
	model       Model
	tools       map[string]Tool
	definitions []llm.ToolDefinition
}

// NewLoop constructs an agent loop from immutable dependencies.
func NewLoop(model Model, tools []Tool) (*Loop, error) {
	if model == nil {
		return nil, fmt.Errorf("agent: model is required")
	}

	toolIndex := make(map[string]Tool, len(tools))
	definitions := make([]llm.ToolDefinition, 0, len(tools))
	for index, tool := range tools {
		if tool == nil {
			return nil, fmt.Errorf("agent: tool %d is nil", index)
		}

		definition := tool.Definition()
		if strings.TrimSpace(definition.Name) == "" {
			return nil, fmt.Errorf("agent: tool %d name is required", index)
		}
		if _, exists := toolIndex[definition.Name]; exists {
			return nil, fmt.Errorf("agent: duplicate tool name %q", definition.Name)
		}
		if err := validateToolSchema(definition); err != nil {
			return nil, fmt.Errorf("agent: tool %d: %w", index, err)
		}

		definition.InputSchema = slices.Clone(definition.InputSchema)
		toolIndex[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	return &Loop{
		model:       model,
		tools:       toolIndex,
		definitions: definitions,
	}, nil
}

// Run executes one agent run. It does not retain mutable run state.
func (l *Loop) Run(ctx context.Context, input RunInput, sink AgentEventSink) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("agent: context is required")
	}
	if err := input.Prompt.Validate(); err != nil {
		return Result{}, fmt.Errorf("agent: validate prompt: %w", err)
	}

	initialResult := Result{Prompt: input.Prompt}
	history := slices.Clone(input.History)
	history = append(history, input.Prompt)
	execution := runExecution{
		loop:    l,
		input:   input,
		sink:    sink,
		history: history,
		result:  initialResult,
	}
	request, err := execution.request()
	if err != nil {
		runErr := fmt.Errorf("agent: prepare initial request: %w", err)
		return finalizeFailedResult(initialResult, input.Model, runErr), runErr
	}
	if err := checkCompactionThreshold(request); err != nil {
		runErr := fmt.Errorf("agent: protect initial request: %w", err)
		return finalizeFailedResult(initialResult, input.Model, runErr), runErr
	}
	if err := request.Validate(); err != nil {
		runErr := fmt.Errorf("agent: validate initial request: %w", err)
		return finalizeFailedResult(initialResult, input.Model, runErr), runErr
	}

	result, runErr := execution.run(ctx)
	return finalizeFailedResult(result, input.Model, runErr), runErr
}

type runExecution struct {
	loop    *Loop
	input   RunInput
	sink    AgentEventSink
	history []llm.AgentMessage
	result  Result
}

func (e *runExecution) run(ctx context.Context) (Result, error) {
	if err := e.emit(ctx, AgentEvent{Type: EventTypeAgentStart}); err != nil {
		return e.result, err
	}

	turnNumber := 1
	if err := e.emit(ctx, AgentEvent{Type: EventTypeTurnStart, TurnNumber: turnNumber}); err != nil {
		return e.result, err
	}
	prompt := e.input.Prompt
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeMessageStart,
		TurnNumber: turnNumber,
		Message:    prompt,
	}); err != nil {
		return e.result, err
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeMessageEnd,
		TurnNumber: turnNumber,
		Message:    prompt,
	}); err != nil {
		return e.result, err
	}

	for {
		outcome, streamErr := e.streamAssistant(ctx, turnNumber)
		if isEventSinkError(streamErr) {
			return e.result, streamErr
		}
		if !outcome.complete {
			return e.finishIncompleteTurn(ctx, turnNumber, streamErr)
		}

		turn := Turn{
			Number:      turnNumber,
			Assistant:   outcome.message,
			ToolResults: []llm.ToolResultMessage{},
		}
		e.history = append(e.history, outcome.message)

		runErr := errors.Join(outcome.terminalErr, streamErr)
		if runErr == nil {
			calls, err := extractToolCalls(outcome.message)
			if err != nil {
				runErr = err
			} else if len(calls) > 0 {
				var toolErr error
				if outcome.message.StopReason == llm.StopReasonLength {
					turn.ToolResults, toolErr = e.failTruncatedToolCalls(
						ctx,
						turnNumber,
						calls,
					)
				} else {
					turn.ToolResults, toolErr = e.executeTools(ctx, turnNumber, calls)
				}
				if isEventSinkError(toolErr) {
					e.result.Turns = append(e.result.Turns, turn)
					return e.result, toolErr
				}
				runErr = toolErr
			}
		}

		e.result.Turns = append(e.result.Turns, turn)
		completedTurn := e.result.Turns[len(e.result.Turns)-1]
		if err := e.emit(ctx, AgentEvent{
			Type:        EventTypeTurnEnd,
			TurnNumber:  turnNumber,
			Message:     completedTurn.Assistant,
			ToolResults: slices.Clone(completedTurn.ToolResults),
			Err:         runErr,
		}); err != nil {
			return e.result, errors.Join(runErr, err)
		}
		if runErr != nil {
			return e.finishRun(ctx, runErr)
		}
		if len(turn.ToolResults) == 0 {
			return e.finishRun(ctx, nil)
		}

		turnNumber++
		if err := e.emit(ctx, AgentEvent{Type: EventTypeTurnStart, TurnNumber: turnNumber}); err != nil {
			return e.result, err
		}
	}
}

type assistantOutcome struct {
	message     llm.AssistantMessage
	terminalErr error
	complete    bool
}

func (e *runExecution) streamAssistant(
	ctx context.Context,
	turnNumber int,
) (assistantOutcome, error) {
	request, err := e.request()
	if err != nil {
		return assistantOutcome{}, fmt.Errorf(
			"agent: prepare turn %d request: %w",
			turnNumber,
			err,
		)
	}
	if err := request.Validate(); err != nil {
		return assistantOutcome{}, fmt.Errorf("agent: validate turn %d request: %w", turnNumber, err)
	}

	stream, err := e.loop.model.Stream(ctx, request)
	if err != nil {
		return assistantOutcome{}, fmt.Errorf("agent: start model stream: %w", err)
	}
	if stream == nil {
		return assistantOutcome{}, fmt.Errorf("%w: model returned a nil stream", ErrProtocol)
	}

	outcome, consumeErr := e.consumeAssistant(ctx, turnNumber, request.Model, stream)
	if closeErr := stream.Close(); closeErr != nil {
		consumeErr = errors.Join(consumeErr, fmt.Errorf("agent: close model stream: %w", closeErr))
	}
	return outcome, consumeErr
}

func (e *runExecution) consumeAssistant(
	ctx context.Context,
	turnNumber int,
	model llm.Model,
	stream llm.Stream,
) (assistantOutcome, error) {
	started := false
	for {
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return assistantOutcome{}, ctxErr
				}
				return assistantOutcome{}, fmt.Errorf(
					"%w: stream ended before a terminal event",
					ErrProtocol,
				)
			}
			return assistantOutcome{}, fmt.Errorf("agent: read model stream: %w", err)
		}

		switch event.Type {
		case llm.EventTypeStart:
			if started {
				return assistantOutcome{}, fmt.Errorf("%w: duplicate start event", ErrProtocol)
			}
			started = true
			message := llm.NewAssistantMessage(model)
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeMessageStart,
				TurnNumber: turnNumber,
				Message:    message,
			}); err != nil {
				return assistantOutcome{}, err
			}
		case llm.EventTypeDone, llm.EventTypeError:
			if !started {
				return assistantOutcome{}, fmt.Errorf(
					"%w: terminal event arrived before start",
					ErrProtocol,
				)
			}
			if event.Message == nil {
				return assistantOutcome{}, fmt.Errorf(
					"%w: terminal event has no assistant message",
					ErrProtocol,
				)
			}

			message := *event.Message
			if err := message.Validate(); err != nil {
				return assistantOutcome{}, fmt.Errorf(
					"%w: validate terminal assistant message: %v",
					ErrProtocol,
					err,
				)
			}
			terminalErr := terminalError(ctx, event, message)
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeMessageEnd,
				TurnNumber: turnNumber,
				Message:    message,
				Err:        terminalErr,
			}); err != nil {
				return assistantOutcome{}, err
			}
			return assistantOutcome{
				message:     message,
				terminalErr: terminalErr,
				complete:    true,
			}, nil
		case llm.EventTypeTextStart,
			llm.EventTypeTextDelta,
			llm.EventTypeTextEnd,
			llm.EventTypeThinkingStart,
			llm.EventTypeThinkingDelta,
			llm.EventTypeThinkingEnd,
			llm.EventTypeToolCallStart,
			llm.EventTypeToolCallDelta,
			llm.EventTypeToolCallEnd,
			llm.EventTypeUsage:
			if !started {
				return assistantOutcome{}, fmt.Errorf(
					"%w: %s event arrived before start",
					ErrProtocol,
					event.Type,
				)
			}
			streamEvent := event
			if err := e.emit(ctx, AgentEvent{
				Type:                  EventTypeMessageUpdate,
				TurnNumber:            turnNumber,
				AssistantMessageEvent: &streamEvent,
			}); err != nil {
				return assistantOutcome{}, err
			}
		default:
			return assistantOutcome{}, fmt.Errorf(
				"%w: unsupported stream event type %q",
				ErrProtocol,
				event.Type,
			)
		}
	}
}

func (e *runExecution) failTruncatedToolCalls(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
) ([]llm.ToolResultMessage, error) {
	results := make([]llm.ToolResultMessage, 0, len(calls))
	for index := range calls {
		call := calls[index]
		callErr := fmt.Errorf(
			"tool %q was not executed: the assistant response reached the output token limit, "+
				"so its arguments may be truncated; reissue the tool call with complete arguments",
			call.Name,
		)
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionStart,
			TurnNumber: turnNumber,
			ToolCall:   &call,
		}); err != nil {
			return results, err
		}

		message := newErrorToolResult(call, callErr)
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionEnd,
			TurnNumber: turnNumber,
			ToolCall:   &call,
			ToolResult: &message,
			Err:        callErr,
		}); err != nil {
			return results, err
		}
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return results, err
		}

		results = append(results, message)
		e.history = append(e.history, message)
	}
	return results, nil
}

func (e *runExecution) executeTools(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
) ([]llm.ToolResultMessage, error) {
	results := make([]llm.ToolResultMessage, 0, len(calls))
	for index := range calls {
		call := calls[index]
		if ctxErr := ctx.Err(); ctxErr != nil {
			remaining, err := e.syntheticToolResults(
				ctx,
				turnNumber,
				calls[index:],
				ctxErr.Error(),
			)
			results = append(results, remaining...)
			return results, errors.Join(ctxErr, err)
		}

		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionStart,
			TurnNumber: turnNumber,
			ToolCall:   &call,
		}); err != nil {
			return results, err
		}

		message, toolErr := e.executeTool(ctx, call)
		results = append(results, message)
		e.history = append(e.history, message)
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionEnd,
			TurnNumber: turnNumber,
			ToolCall:   &call,
			ToolResult: &message,
			Err:        toolErr,
		}); err != nil {
			return results, err
		}
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return results, err
		}
	}
	return results, ctx.Err()
}

func (e *runExecution) executeTool(
	ctx context.Context,
	call llm.ToolCall,
) (llm.ToolResultMessage, error) {
	tool, exists := e.loop.tools[call.Name]
	if !exists {
		err := fmt.Errorf("tool %q is not available", call.Name)
		return newErrorToolResult(call, err), err
	}

	result, err := tool.Execute(ctx, call)
	if err != nil {
		wrapped := fmt.Errorf("tool %q failed: %w", call.Name, err)
		return newErrorToolResult(call, wrapped), wrapped
	}

	result.CallID = call.ID
	result.Name = call.Name
	message, err := llm.NewToolResultMessage(result)
	if err != nil {
		wrapped := fmt.Errorf("tool %q returned an invalid result: %w", call.Name, err)
		return newErrorToolResult(call, wrapped), wrapped
	}
	return message, nil
}

func (e *runExecution) syntheticToolResults(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
	reason string,
) ([]llm.ToolResultMessage, error) {
	results := make([]llm.ToolResultMessage, 0, len(calls))
	for index := range calls {
		call := calls[index]
		message := newErrorToolResult(call, errors.New(reason))
		results = append(results, message)
		e.history = append(e.history, message)
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return results, err
		}
	}
	return results, nil
}

func (e *runExecution) emitToolResultMessage(
	ctx context.Context,
	turnNumber int,
	call llm.ToolCall,
	message llm.ToolResultMessage,
) error {
	for _, eventType := range []EventType{EventTypeMessageStart, EventTypeMessageEnd} {
		if err := e.emit(ctx, AgentEvent{
			Type:       eventType,
			TurnNumber: turnNumber,
			ToolCall:   &call,
			Message:    message,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *runExecution) request() (llm.Request, error) {
	definitions := make([]llm.ToolDefinition, len(e.loop.definitions))
	for index, definition := range e.loop.definitions {
		definition.InputSchema = slices.Clone(definition.InputSchema)
		definitions[index] = definition
	}
	messages, err := llm.AgentMessagesToMessages(e.history)
	if err != nil {
		return llm.Request{}, fmt.Errorf("agent: project history: %w", err)
	}
	request := llm.Request{
		Model:        e.input.Model,
		SystemPrompt: e.input.SystemPrompt,
		Messages:     messages,
		Tools:        definitions,
		Options:      e.input.Options,
	}
	return protectRequestContext(request)
}

func (e *runExecution) finishIncompleteTurn(
	ctx context.Context,
	turnNumber int,
	runErr error,
) (Result, error) {
	if runErr == nil {
		runErr = fmt.Errorf("%w: assistant turn did not complete", ErrProtocol)
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnEnd,
		TurnNumber: turnNumber,
		Err:        runErr,
	}); err != nil {
		return e.result, errors.Join(runErr, err)
	}
	return e.finishRun(ctx, runErr)
}

func (e *runExecution) finishRun(ctx context.Context, runErr error) (Result, error) {
	e.result = finalizeFailedResult(e.result, e.input.Model, runErr)
	result := e.result
	if err := e.emit(ctx, AgentEvent{
		Type:     EventTypeAgentEnd,
		Messages: result.Messages(),
		Err:      runErr,
	}); err != nil {
		return e.result, errors.Join(runErr, err)
	}
	return e.result, runErr
}

func finalizeFailedResult(
	result Result,
	model llm.Model,
	runErr error,
) Result {
	if runErr == nil {
		return result
	}

	terminalText, stopReason := terminalFailure(runErr)
	result = pairUnfinishedToolCalls(result, terminalText)
	if resultEndsAtAssistant(result) {
		return result
	}
	if err := result.Prompt.Validate(); err != nil {
		return result
	}

	message := llm.NewAssistantMessage(model)
	message.Content = []llm.ContentPart{
		llm.NewTextContent(terminalText).Part(),
	}
	message.StopReason = stopReason
	message.ErrorMessage = terminalText
	if err := message.Validate(); err != nil {
		return result
	}

	turnNumber := 1
	if len(result.Turns) > 0 {
		turnNumber = result.Turns[len(result.Turns)-1].Number + 1
	}
	result.Turns = append(result.Turns, Turn{
		Number:      turnNumber,
		Assistant:   message,
		ToolResults: []llm.ToolResultMessage{},
	})
	return result
}

func pairUnfinishedToolCalls(result Result, terminalText string) Result {
	for turnIndex := range result.Turns {
		turn := &result.Turns[turnIndex]
		calls, err := extractToolCalls(turn.Assistant)
		if err != nil {
			continue
		}

		recorded := make(map[string]struct{}, len(turn.ToolResults))
		for _, toolResult := range turn.ToolResults {
			recorded[toolResult.ToolCallID] = struct{}{}
		}
		for _, call := range calls {
			if _, exists := recorded[call.ID]; exists {
				continue
			}
			message := newErrorToolResult(
				call,
				fmt.Errorf(
					"tool %q was not executed because the %s",
					call.Name,
					terminalText,
				),
			)
			turn.ToolResults = append(turn.ToolResults, message)
			recorded[call.ID] = struct{}{}
		}
	}
	return result
}

func resultEndsAtAssistant(result Result) bool {
	if len(result.Turns) == 0 {
		return false
	}
	last := result.Turns[len(result.Turns)-1]
	if len(last.ToolResults) != 0 {
		return false
	}
	for _, part := range last.Assistant.Content {
		if part.Type == llm.ContentTypeToolCall {
			return false
		}
	}
	return true
}

func terminalFailure(runErr error) (string, llm.StopReason) {
	switch {
	case errors.Is(runErr, context.Canceled):
		return "agent run canceled before completion", llm.StopReasonAborted
	case errors.Is(runErr, context.DeadlineExceeded):
		return "agent run deadline exceeded before completion", llm.StopReasonAborted
	case errors.Is(runErr, ErrContextLimit):
		return "agent run stopped after reaching the context limit", llm.StopReasonError
	default:
		return "agent run failed before completion", llm.StopReasonError
	}
}

func (e *runExecution) emit(ctx context.Context, event AgentEvent) error {
	if e.sink == nil {
		return nil
	}
	if err := e.sink(ctx, event); err != nil {
		return eventSinkError{eventType: event.Type, err: err}
	}
	return nil
}

type eventSinkError struct {
	eventType EventType
	err       error
}

func (e eventSinkError) Error() string {
	return fmt.Sprintf("agent: emit %s event: %v", e.eventType, e.err)
}

func (e eventSinkError) Unwrap() error {
	return e.err
}

func isEventSinkError(err error) bool {
	var target eventSinkError
	return errors.As(err, &target)
}

func validateToolSchema(definition llm.ToolDefinition) error {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		return fmt.Errorf("tool %q input schema must be a json object: %w", definition.Name, err)
	}
	if schema == nil {
		return fmt.Errorf("tool %q input schema must be a json object", definition.Name)
	}
	return nil
}

func extractToolCalls(message llm.AssistantMessage) ([]llm.ToolCall, error) {
	calls := make([]llm.ToolCall, 0)
	callIDs := make(map[string]struct{})
	for _, part := range message.Content {
		if part.Type != llm.ContentTypeToolCall {
			continue
		}
		if part.ToolCall == nil {
			return nil, fmt.Errorf("%w: tool-call content has no call", ErrProtocol)
		}
		if _, exists := callIDs[part.ToolCall.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate tool call id %q", ErrProtocol, part.ToolCall.ID)
		}
		callIDs[part.ToolCall.ID] = struct{}{}
		call := *part.ToolCall
		call.Arguments = slices.Clone(call.Arguments)
		calls = append(calls, call)
	}
	return calls, nil
}

func terminalError(ctx context.Context, event llm.Event, message llm.AssistantMessage) error {
	if event.Type != llm.EventTypeError &&
		message.StopReason != llm.StopReasonError &&
		message.StopReason != llm.StopReasonAborted {
		return nil
	}
	if event.Err != nil {
		return fmt.Errorf("agent: model stream failed: %w", event.Err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if message.ErrorMessage != "" {
		return fmt.Errorf("agent: model stream failed: %s", message.ErrorMessage)
	}
	return fmt.Errorf("agent: model stream stopped with reason %q", message.StopReason)
}

func newErrorToolResult(call llm.ToolCall, err error) llm.ToolResultMessage {
	message, messageErr := llm.NewToolResultMessage(llm.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []llm.ContentPart{
			llm.NewTextContent(err.Error()).Part(),
		},
		IsError: true,
	})
	if messageErr != nil {
		panic(fmt.Sprintf("agent: construct internal tool error result: %v", messageErr))
	}
	return message
}
