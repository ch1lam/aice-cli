package agent

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type assistantOutcome struct {
	message     llm.AssistantMessage
	terminalErr error
	complete    bool
	// dispatched reports that the request reached the model; a failure before
	// it is not a model attempt and records nothing.
	dispatched bool
}

func (e *runExecution) streamAssistant(
	ctx context.Context,
	allowCompaction bool,
) (assistantOutcome, error) {
	turnNumber := e.lifecycle.turn
	e.requestConfigID = ""
	if err := e.checkBudget(ctx); err != nil {
		return assistantOutcome{}, err
	}
	if err := e.checkMaxTurns(); err != nil {
		return assistantOutcome{}, err
	}
	request, err := e.prepareRequest(ctx, allowCompaction)
	if err != nil {
		return assistantOutcome{}, fmt.Errorf(
			"agent: prepare turn %d request: %w",
			turnNumber,
			err,
		)
	}

	if err := e.checkBudget(ctx); err != nil {
		return assistantOutcome{}, err
	}
	if e.input.RequestRecorder != nil {
		id, err := e.input.RequestRecorder(ctx, e.requestConfig().Clone())
		if err != nil {
			return assistantOutcome{}, fmt.Errorf("agent: record turn %d request configuration: %w", turnNumber, err)
		}
		e.requestConfigID = id
	}
	e.turnsUsed++
	if e.input.ObserveTools != nil {
		refs := make([]ToolReference, 0, len(e.catalogTools))
		for _, definition := range request.Tools {
			if entry, ok := e.catalogTools[definition.Name]; ok {
				refs = append(refs, entry.Reference)
			}
		}
		e.input.ObserveTools(refs)
	}
	stream, err := e.loop.model.Stream(ctx, request)
	if err != nil {
		return assistantOutcome{dispatched: true}, fmt.Errorf("agent: start model stream: %w", err)
	}
	if stream == nil {
		return assistantOutcome{dispatched: true}, fmt.Errorf("%w: model returned a nil stream", ErrProtocol)
	}

	outcome, consumeErr := e.consumeAssistant(ctx, request.Model, stream)
	outcome.dispatched = true
	if outcome.complete {
		outcome.message.RequestConfig = e.requestConfigID
	}
	if closeErr := stream.Close(); closeErr != nil {
		consumeErr = errors.Join(consumeErr, fmt.Errorf("agent: close model stream: %w", closeErr))
	}
	return outcome, consumeErr
}

func (e *runExecution) consumeAssistant(
	ctx context.Context,
	model llm.Model,
	stream llm.Stream,
) (assistantOutcome, error) {
	turnNumber := e.lifecycle.turn
	var usage llm.Usage
	defer func() { e.addUsage(usage) }()
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
			return assistantOutcome{}, fmt.Errorf(
				"agent: read model stream: %w",
				err,
			)
		}

		switch event.Type {
		case llm.EventTypeStart:
			if e.lifecycle.assistantStarted {
				return assistantOutcome{}, fmt.Errorf("%w: duplicate start event", ErrProtocol)
			}
			e.lifecycle.assistantStarted = true
			message := llm.NewAssistantMessage(model)
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeMessageStart,
				TurnNumber: turnNumber,
				Message:    message,
			}); err != nil {
				return assistantOutcome{}, err
			}
		case llm.EventTypeDone, llm.EventTypeError:
			if !e.lifecycle.assistantStarted {
				// A provider can fail before its first item; keep that error
				// so the retry policy judges it rather than the missing start.
				if event.Type == llm.EventTypeError && event.Err != nil {
					return assistantOutcome{}, fmt.Errorf("agent: model stream failed: %w", event.Err)
				}
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
			if message.Usage.TotalTokens > 0 || message.Usage.InputTokens > 0 || message.Usage.OutputTokens > 0 || message.Usage.CacheReadTokens > 0 || message.Usage.CacheWriteTokens > 0 {
				usage = message.Usage
			}
			if err := message.Validate(); err != nil {
				return assistantOutcome{}, fmt.Errorf(
					"%w: validate terminal assistant message: %v",
					ErrProtocol,
					err,
				)
			}
			terminalErr := terminalError(ctx, event, message)
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
			if !e.lifecycle.assistantStarted {
				return assistantOutcome{}, fmt.Errorf(
					"%w: %s event arrived before start",
					ErrProtocol,
					event.Type,
				)
			}
			if event.Type == llm.EventTypeUsage && event.Usage != nil {
				usage = *event.Usage
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
