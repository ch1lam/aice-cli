package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

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

		message, err := newErrorToolResult(call, callErr)
		if err != nil {
			return results, err
		}
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
				true,
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
	// Built-in guard: deny or ask before the tool ever starts. This preserves
	// the "pair every tool call with one result" invariant while preventing
	// the side effect. Ask is resolved via the injected handler or fails closed.
	if e.loop.guard != nil {
		res, err := e.loop.guard.Check(ctx, call)
		if err != nil {
			return newErrorToolResult(call, fmt.Errorf("guard: %w", err))
		}
		switch res.Decision {
		case GuardDeny:
			reason := res.Reason
			if reason == "" {
				reason = fmt.Sprintf("tool %q blocked by guard rule %q", call.Name, res.RuleID)
			}
			return newErrorToolResult(call, fmt.Errorf("%s", reason))
		case GuardAsk:
			decision := GuardDeny
			if e.loop.guardAsk != nil {
				// Pass full GuardResult so the handler can display reason/rule.
				askRes := GuardResult{
					Decision: GuardAsk,
					Reason:   res.Reason,
					RuleID:   res.RuleID,
					Action: GuardAction{
						Kind:     res.Action.Kind,
						Path:     res.Action.Path,
						Command:  res.Action.Command,
						ToolName: res.Action.ToolName,
					},
				}
				d, err := e.loop.guardAsk(ctx, call, askRes)
				if err != nil {
					return newErrorToolResult(call, fmt.Errorf("guard ask: %w", err))
				}
				decision = d
			}
			if decision != GuardAllow {
				reason := res.Reason
				if reason == "" {
					reason = fmt.Sprintf("tool %q requires confirmation (rule %q)", call.Name, res.RuleID)
				}
				return newErrorToolResult(call, fmt.Errorf("%s", reason))
			}
		}
	}

	tool, exists := e.loop.tools[call.Name]
	if !exists {
		err := fmt.Errorf("tool %q is not available", call.Name)
		return newErrorToolResult(call, err)
	}

	result, err := tool.Execute(ctx, call)
	if err != nil {
		wrapped := fmt.Errorf("tool %q failed: %w", call.Name, err)
		return newErrorToolResult(call, wrapped)
	}

	result.CallID = call.ID
	result.Name = call.Name
	message, err := llm.NewToolResultMessage(result)
	if err != nil {
		wrapped := fmt.Errorf("tool %q returned an invalid result: %w", call.Name, err)
		return newErrorToolResult(call, wrapped)
	}
	return message, nil
}

func (e *runExecution) syntheticToolResults(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
	reason string,
	recordHistory bool,
) ([]llm.ToolResultMessage, error) {
	results := make([]llm.ToolResultMessage, 0, len(calls))
	for index := range calls {
		call := calls[index]
		message, err := newErrorToolResult(call, errors.New(reason))
		if err != nil {
			return results, err
		}
		results = append(results, message)
		if recordHistory {
			e.history = append(e.history, message)
		}
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

func newErrorToolResult(call llm.ToolCall, err error) (llm.ToolResultMessage, error) {
	message, messageErr := llm.NewToolResultMessage(llm.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []llm.ContentPart{
			llm.NewTextContent(err.Error()).Part(),
		},
		IsError: true,
	})
	if messageErr != nil {
		return llm.ToolResultMessage{}, fmt.Errorf(
			"agent: construct internal tool error result: %w",
			messageErr,
		)
	}
	return message, nil
}
