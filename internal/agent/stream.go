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
	started     bool
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
					return assistantOutcome{started: started}, ctxErr
				}
				return assistantOutcome{started: started}, fmt.Errorf(
					"%w: stream ended before a terminal event",
					ErrProtocol,
				)
			}
			return assistantOutcome{started: started}, fmt.Errorf(
				"agent: read model stream: %w",
				err,
			)
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
				started:     true,
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
