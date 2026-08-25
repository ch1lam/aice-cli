package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Loop runs provider-neutral model rounds and tool execution. It is safe to
// reuse as long as the supplied Model and Tools are safe for concurrent use.
type Loop struct {
	model       Model
	tools       map[string]Tool
	definitions []llm.ToolDefinition
	retry       RetryPolicy
	guard       Guard
	guardAsk    GuardAskHandler
}

// GuardAskReply is the user's resolution of one guard Ask.
type GuardAskReply struct {
	Decision GuardDecision // GuardAllow or GuardDeny
	Feedback string        // optional user note included in the deny tool result
}

// GuardAskHandler is called when a guard returns Ask. Returning a reply with
// GuardAllow proceeds; GuardDeny blocks. Feedback on a deny reply is copied
// into the error tool result. The handler may block for user confirmation.
// A nil handler fails closed by denying.
type GuardAskHandler func(ctx context.Context, call llm.ToolCall, result GuardResult) (GuardAskReply, error)

// WithGuard installs an execution gate consulted before each tool call.
// It is the intrinsic guard (internal/guard), not a plugin. A non-nil
// Guard is required when the loop has tools.
func WithGuard(g Guard) LoopOption {
	return func(l *Loop) error {
		l.guard = g
		return nil
	}
}

// WithGuardAskHandler installs the interactive Ask resolver. It is called
// synchronously inside ExecuteTool when Guard returns Ask. A nil handler
// fails closed by denying.
func WithGuardAskHandler(h GuardAskHandler) LoopOption {
	return func(l *Loop) error {
		l.guardAsk = h
		return nil
	}
}

// NewLoop constructs an agent loop from immutable dependencies.
// A non-empty tool set requires a non-nil Guard via WithGuard. An empty
// tool set does not, so compaction can run without tools or a gate.
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
	if len(tools) > 0 && loop.guard == nil {
		return nil, fmt.Errorf("agent: tools require a guard")
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
	execution := runExecution{
		loop:    l,
		input:   input,
		sink:    sink,
		history: slices.Clone(input.History),
		result:  initialResult,
	}
	if err := execution.prepareInputContext(ctx, input.Prompt); err != nil {
		runErr := fmt.Errorf("agent: prepare initial request: %w", err)
		return finalizeRunResult(initialResult, input.Model, runErr, nil)
	}

	result, runErr := execution.run(ctx)
	return finalizeRunResult(
		result,
		input.Model,
		runErr,
		execution.takePendingInputs(),
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
	loop             *Loop
	input            RunInput
	sink             AgentEventSink
	history          []llm.AgentMessage
	result           Result
	pendingInputs    []llm.UserMessage
	interactionStart int
}

func (e *runExecution) run(ctx context.Context) (Result, error) {
	if err := e.emit(ctx, AgentEvent{Type: EventTypeAgentStart}); err != nil {
		return e.result, err
	}

	turnNumber := 1
	if err := e.startInputTurn(ctx, turnNumber, InputMessage{
		Message: e.input.Prompt,
	}, InputKindUnknown); err != nil {
		return e.result, err
	}

	for {
		result, err, settled := e.runInteraction(ctx, &turnNumber)
		if !settled {
			return result, err
		}
		if err := e.finishInteraction(ctx); err != nil {
			return e.result, err
		}
		result, err, follows := e.pollFollowUp(ctx, &turnNumber)
		if !follows {
			return result, err
		}
	}
}

func (e *runExecution) runInteraction(
	ctx context.Context,
	turnNumber *int,
) (Result, error, bool) {
	retryAttempt := 0
	for {
		outcome, streamErr := e.streamAssistant(ctx, *turnNumber)
		if isEventSinkError(streamErr) {
			return e.result, streamErr, false
		}
		if !outcome.complete {
			if streamErr == nil {
				streamErr = fmt.Errorf("%w: assistant turn did not complete", ErrProtocol)
			}
			currentTurn := *turnNumber
			retry := e.retryTurn(
				ctx,
				retryAttempt,
				currentTurn,
				streamErr,
				func() error {
					return e.recordIncompleteAttempt(
						ctx,
						currentTurn,
						outcome.started,
						streamErr,
					)
				},
			)
			if retry.waitErr != nil {
				result, err := e.finishRun(ctx, retry.waitErr)
				return result, err, false
			}
			if retry.stopErr != nil {
				return e.result, retry.stopErr, false
			}
			if !retry.retry {
				result, err := e.finishIncompleteTurn(ctx, currentTurn, streamErr)
				return result, err, false
			}
			retryAttempt, *turnNumber = retry.nextAttempt, retry.nextTurn
			continue
		}

		result, err, next := e.settleToolsAndSteering(
			ctx,
			turnNumber,
			&retryAttempt,
			outcome,
			streamErr,
		)
		switch next {
		case settleStop:
			return result, err, false
		case settleContinue:
			continue
		case settleDone:
			return e.result, nil, true
		}
	}
}

type settleNext int

const (
	settleStop settleNext = iota
	settleContinue
	settleDone
)

func (e *runExecution) settleToolsAndSteering(
	ctx context.Context,
	turnNumber *int,
	retryAttempt *int,
	outcome assistantOutcome,
	streamErr error,
) (Result, error, settleNext) {
	turn := ModelRound{
		Number:      *turnNumber,
		Inputs:      e.takePendingInputs(),
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
				*turnNumber,
				calls,
				"model request failed before tool execution",
				false,
			)
			if isEventSinkError(err) {
				e.result.ModelRounds = append(e.result.ModelRounds, turn)
				return e.result, err, settleStop
			}
			runErr = errors.Join(runErr, err)
		}
	} else {
		e.history = append(e.history, outcome.message)
		if *retryAttempt > 0 {
			if err := e.emitRetryEnd(ctx, *retryAttempt, true, nil); err != nil {
				return e.result, err, settleStop
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
					*turnNumber,
					calls,
				)
			} else {
				turn.ToolResults, toolErr = e.executeTools(ctx, *turnNumber, calls)
			}
			if isEventSinkError(toolErr) {
				e.result.ModelRounds = append(e.result.ModelRounds, turn)
				return e.result, toolErr, settleStop
			}
			runErr = toolErr
		}
	}

	e.result.ModelRounds = append(e.result.ModelRounds, turn)
	completedTurn := e.result.ModelRounds[len(e.result.ModelRounds)-1]
	if err := e.emit(ctx, AgentEvent{
		Type:        EventTypeTurnEnd,
		TurnNumber:  *turnNumber,
		Message:     completedTurn.Assistant,
		ToolResults: slices.Clone(completedTurn.ToolResults),
		Err:         runErr,
	}); err != nil {
		return e.result, errors.Join(runErr, err), settleStop
	}
	if modelErr != nil {
		retry := e.retryTurn(
			ctx,
			*retryAttempt,
			*turnNumber,
			runErr,
			nil,
		)
		if retry.waitErr != nil {
			result, err := e.finishRun(ctx, retry.waitErr)
			return result, err, settleStop
		}
		if retry.stopErr != nil {
			return e.result, retry.stopErr, settleStop
		}
		if !retry.retry {
			result, err := e.finishRun(ctx, runErr)
			return result, err, settleStop
		}
		*retryAttempt, *turnNumber = retry.nextAttempt, retry.nextTurn
		return e.result, nil, settleContinue
	}
	if runErr != nil {
		result, err := e.finishRun(ctx, runErr)
		return result, err, settleStop
	}

	steering, steers, steeringErr := e.nextInput(
		e.input.Steering,
		"steering",
	)
	if steeringErr != nil {
		result, err := e.finishRun(ctx, steeringErr)
		return result, err, settleStop
	}
	if steers {
		(*turnNumber)++
		*retryAttempt = 0
		e.history = append(e.history, steering.Message)
		e.pendingInputs = append(e.pendingInputs, steering.Message)
		if err := e.startInputTurn(
			ctx,
			*turnNumber,
			steering,
			InputKindSteering,
		); err != nil {
			return e.result, err, settleStop
		}
		return e.result, nil, settleContinue
	}
	if len(turn.ToolResults) == 0 {
		return e.result, nil, settleDone
	}

	*retryAttempt = 0
	(*turnNumber)++
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnStart,
		TurnNumber: *turnNumber,
	}); err != nil {
		return e.result, err, settleStop
	}
	return e.result, nil, settleContinue
}

func (e *runExecution) pollFollowUp(
	ctx context.Context,
	turnNumber *int,
) (Result, error, bool) {
	followUp, followsUp, followUpErr := e.nextInput(
		e.input.FollowUp,
		"follow-up",
	)
	if followUpErr != nil {
		result, err := e.finishRun(ctx, followUpErr)
		return result, err, false
	}
	if !followsUp {
		result, err := e.finishRun(ctx, nil)
		return result, err, false
	}

	// Keep the follow-up pending before compaction so a failed compaction
	// still produces a durable terminal interaction for the accepted input.
	e.pendingInputs = append(e.pendingInputs, followUp.Message)
	if err := e.prepareInputContext(ctx, followUp.Message); err != nil {
		result, err := e.finishRun(ctx, err)
		return result, err, false
	}

	(*turnNumber)++
	if err := e.startInputTurn(
		ctx,
		*turnNumber,
		followUp,
		InputKindFollowUp,
	); err != nil {
		return e.result, err, false
	}
	return e.result, nil, true
}

func (e *runExecution) prepareInputContext(
	ctx context.Context,
	input llm.AgentMessage,
) error {
	baseHistory := slices.Clone(e.history)
	candidate := append(slices.Clone(baseHistory), input)
	request, err := e.requestForHistory(candidate)
	if err == nil {
		err = checkCompactionThreshold(request)
	}
	if err == nil {
		if err := request.Validate(); err != nil {
			return fmt.Errorf("agent: validate input request: %w", err)
		}
		e.history = candidate
		return nil
	}
	if !errors.Is(err, ErrContextLimit) || e.input.Compactor == nil {
		return err
	}

	compacted, compactErr := e.input.Compactor(ctx, baseHistory)
	if compactErr != nil {
		return errors.Join(
			err,
			fmt.Errorf("agent: compact complete history: %w", compactErr),
		)
	}
	candidate = append(slices.Clone(compacted), input)
	request, err = e.requestForHistory(candidate)
	if err == nil {
		err = checkCompactionThreshold(request)
	}
	if err != nil {
		return fmt.Errorf("agent: protect request after compaction: %w", err)
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("agent: validate request after compaction: %w", err)
	}
	e.history = candidate
	return nil
}

func (e *runExecution) startInputTurn(
	ctx context.Context,
	turnNumber int,
	input InputMessage,
	kind InputKind,
) error {
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnStart,
		TurnNumber: turnNumber,
	}); err != nil {
		return err
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeMessageStart,
		TurnNumber: turnNumber,
		InputID:    input.ID,
		InputKind:  kind,
		Message:    input.Message,
	}); err != nil {
		return err
	}
	return e.emit(ctx, AgentEvent{
		Type:       EventTypeMessageEnd,
		TurnNumber: turnNumber,
		InputID:    input.ID,
		InputKind:  kind,
		Message:    input.Message,
	})
}

func (e *runExecution) finishInteraction(ctx context.Context) error {
	messages := e.result.Messages()
	completed := slices.Clone(messages[e.interactionStart:])
	if err := e.emit(ctx, AgentEvent{
		Type:     EventTypeInteractionEnd,
		Messages: completed,
	}); err != nil {
		return err
	}
	e.interactionStart = len(messages)
	return nil
}

func (e *runExecution) nextInput(
	source InputSource,
	label string,
) (InputMessage, bool, error) {
	if source == nil {
		return InputMessage{}, false, nil
	}
	input, ok, err := source()
	if err != nil {
		return InputMessage{}, false, fmt.Errorf(
			"agent: receive %s message: %w",
			label,
			err,
		)
	}
	if !ok {
		return InputMessage{}, false, nil
	}
	if strings.TrimSpace(input.ID) == "" {
		return InputMessage{}, false, fmt.Errorf(
			"agent: %s message id is required",
			label,
		)
	}
	if err := input.Message.Validate(); err != nil {
		return InputMessage{}, false, fmt.Errorf(
			"agent: validate %s message: %w",
			label,
			err,
		)
	}
	return input, true, nil
}

func (e *runExecution) takePendingInputs() []llm.UserMessage {
	inputs := e.pendingInputs
	e.pendingInputs = nil
	return inputs
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
