package agent

import (
	"context"
	"errors"
	"fmt"
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
	retry       RetryPolicy
}

// NewLoop constructs an agent loop from immutable dependencies.
func NewLoop(model Model, tools []Tool, options ...LoopOption) (*Loop, error) {
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
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("agent: tool %d: %w", index, err)
		}

		definition.InputSchema = slices.Clone(definition.InputSchema)
		toolIndex[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	loop := &Loop{
		model:       model,
		tools:       toolIndex,
		definitions: definitions,
		retry:       DefaultRetryPolicy(),
	}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("agent: loop option %d is nil", index)
		}
		if err := option(loop); err != nil {
			return nil, fmt.Errorf("agent: loop option %d: %w", index, err)
		}
	}
	return loop, nil
}

// Run executes one agent run. It does not retain mutable run state.
func (l *Loop) Run(ctx context.Context, input RunInput, sink AgentEventSink) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("agent: context is required")
	}
	if err := validateModel(l.model, input.Model); err != nil {
		return Result{}, fmt.Errorf("agent: validate model: %w", err)
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
		return finalizeRunResult(initialResult, input.Model, runErr, nil)
	}
	if err := checkCompactionThreshold(request); err != nil {
		runErr := fmt.Errorf("agent: protect initial request: %w", err)
		return finalizeRunResult(initialResult, input.Model, runErr, nil)
	}
	if err := request.Validate(); err != nil {
		runErr := fmt.Errorf("agent: validate initial request: %w", err)
		return finalizeRunResult(initialResult, input.Model, runErr, nil)
	}

	result, runErr := execution.run(ctx)
	return finalizeRunResult(
		result,
		input.Model,
		runErr,
		execution.takePendingSteering(),
	)
}

func validateModel(service Model, model llm.Model) error {
	identity, ok := service.(ModelIdentity)
	if !ok {
		return nil
	}
	if model.Provider != identity.ProviderID() {
		return fmt.Errorf(
			"model provider %q does not match the loop model provider %q",
			model.Provider,
			identity.ProviderID(),
		)
	}
	return nil
}

type runExecution struct {
	loop            *Loop
	input           RunInput
	sink            AgentEventSink
	history         []llm.AgentMessage
	result          Result
	pendingSteering []llm.UserMessage
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

	retryAttempt := 0
	for {
		outcome, streamErr := e.streamAssistant(ctx, turnNumber)
		if isEventSinkError(streamErr) {
			return e.result, streamErr
		}
		if !outcome.complete {
			if streamErr == nil {
				streamErr = fmt.Errorf("%w: assistant turn did not complete", ErrProtocol)
			}
			outcome := e.retryTurn(
				ctx,
				retryAttempt,
				turnNumber,
				streamErr,
				func() error {
					return e.recordIncompleteAttempt(ctx, turnNumber, outcome.started, streamErr)
				},
			)
			if outcome.waitErr != nil {
				return e.finishRun(ctx, outcome.waitErr)
			}
			if outcome.stopErr != nil {
				return e.result, outcome.stopErr
			}
			if !outcome.retry {
				return e.finishIncompleteTurn(ctx, turnNumber, streamErr)
			}
			retryAttempt, turnNumber = outcome.nextAttempt, outcome.nextTurn
			continue
		}

		turn := Turn{
			Number:      turnNumber,
			Steering:    e.takePendingSteering(),
			Assistant:   outcome.message,
			ToolResults: []llm.ToolResultMessage{},
		}
		modelErr := errors.Join(outcome.terminalErr, streamErr)
		runErr := modelErr
		if modelErr != nil {
			calls, err := extractToolCalls(outcome.message)
			if err != nil {
				runErr = errors.Join(modelErr, err)
			} else if len(calls) > 0 {
				turn.ToolResults, err = e.syntheticToolResults(
					ctx,
					turnNumber,
					calls,
					"model request failed before tool execution",
					false,
				)
				if isEventSinkError(err) {
					e.result.Turns = append(e.result.Turns, turn)
					return e.result, err
				}
				runErr = errors.Join(runErr, err)
			}
		} else {
			e.history = append(e.history, outcome.message)
			if retryAttempt > 0 {
				if err := e.emitRetryEnd(ctx, retryAttempt, true, nil); err != nil {
					return e.result, err
				}
			}
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
		if modelErr != nil {
			outcome := e.retryTurn(
				ctx,
				retryAttempt,
				turnNumber,
				runErr,
				nil,
			)
			if outcome.waitErr != nil {
				return e.finishRun(ctx, outcome.waitErr)
			}
			if outcome.stopErr != nil {
				return e.result, outcome.stopErr
			}
			if !outcome.retry {
				return e.finishRun(ctx, runErr)
			}
			retryAttempt, turnNumber = outcome.nextAttempt, outcome.nextTurn
			continue
		}
		if runErr != nil {
			return e.finishRun(ctx, runErr)
		}

		steering, steers, steeringErr := e.nextSteering()
		if steeringErr != nil {
			return e.finishRun(ctx, steeringErr)
		}
		if steers {
			turnNumber++
			retryAttempt = 0
			e.history = append(e.history, steering.Message)
			e.pendingSteering = append(e.pendingSteering, steering.Message)
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeTurnStart,
				TurnNumber: turnNumber,
			}); err != nil {
				return e.result, err
			}
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeMessageStart,
				TurnNumber: turnNumber,
				SteeringID: steering.ID,
				Message:    steering.Message,
			}); err != nil {
				return e.result, err
			}
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeMessageEnd,
				TurnNumber: turnNumber,
				SteeringID: steering.ID,
				Message:    steering.Message,
			}); err != nil {
				return e.result, err
			}
			continue
		}
		if len(turn.ToolResults) == 0 {
			return e.finishRun(ctx, nil)
		}

		retryAttempt = 0
		turnNumber++
		if err := e.emit(ctx, AgentEvent{Type: EventTypeTurnStart, TurnNumber: turnNumber}); err != nil {
			return e.result, err
		}
	}
}

func (e *runExecution) nextSteering() (SteeringMessage, bool, error) {
	if e.input.Steering == nil {
		return SteeringMessage{}, false, nil
	}
	steering, ok, err := e.input.Steering()
	if err != nil {
		return SteeringMessage{}, false, fmt.Errorf(
			"agent: receive steering message: %w",
			err,
		)
	}
	if !ok {
		return SteeringMessage{}, false, nil
	}
	if strings.TrimSpace(steering.ID) == "" {
		return SteeringMessage{}, false, fmt.Errorf(
			"agent: steering message ID is required",
		)
	}
	if err := steering.Message.Validate(); err != nil {
		return SteeringMessage{}, false, fmt.Errorf(
			"agent: validate steering message: %w",
			err,
		)
	}
	return steering, true, nil
}

func (e *runExecution) takePendingSteering() []llm.UserMessage {
	steering := e.pendingSteering
	e.pendingSteering = nil
	return steering
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
