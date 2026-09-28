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
) error {
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
			return err
		}

		message, err := e.notDispatchedToolResult(call, callErr)
		if err != nil {
			return err
		}
		if err := e.acceptToolResult(ctx, message, true); err != nil {
			return err
		}
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionEnd,
			TurnNumber: turnNumber,
			ToolCall:   &call,
			ToolResult: &message,
			Err:        callErr,
		}); err != nil {
			return err
		}
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return err
		}
	}
	return nil
}

func (e *runExecution) executeTools(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
) error {
	for index := range calls {
		call := calls[index]
		if ctxErr := e.checkBudget(ctx); ctxErr != nil {
			err := e.syntheticToolResults(
				ctx,
				turnNumber,
				calls[index:],
				ctxErr.Error(),
				true,
			)
			return errors.Join(ctxErr, err)
		}

		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionStart,
			TurnNumber: turnNumber,
			ToolCall:   &call,
		}); err != nil {
			return err
		}

		e.proposal = nil
		message, toolErr := e.executeTool(ctx, call)
		if err := e.acceptToolResult(ctx, message, true); err != nil {
			return err
		}
		if !message.IsError {
			e.pendingSelection = append(e.pendingSelection, e.proposal...)
		}
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeToolExecutionEnd,
			TurnNumber: turnNumber,
			ToolCall:   &call,
			ToolResult: &message,
			Err:        toolErr,
		}); err != nil {
			return err
		}
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (e *runExecution) executeTool(
	ctx context.Context,
	call llm.ToolCall,
) (message llm.ToolResultMessage, executionErr error) {
	started := false
	defer func() {
		if !started && message.IsError && message.Details == nil {
			message.Details = e.notDispatchedDetails(call.Name)
		}
	}()
	if err := e.checkToolVersion(ctx, call.Name); err != nil {
		return newErrorToolResult(call, err)
	}
	var revalidate func(context.Context) error
	// Built-in guard: deny or ask before the tool ever starts. This preserves
	// the "pair every tool call with one result" invariant while preventing
	// the side effect. Ask is resolved via the injected handler or fails closed.
	if e.loop.guard != nil {
		res, err := e.loop.guard.Check(ctx, call)
		if err != nil {
			return newErrorToolResult(call, fmt.Errorf("guard: %w", err))
		}
		if !res.Valid() {
			return newErrorToolResult(call, errors.New("guard returned an invalid result"))
		}
		revalidate = res.Revalidate
		switch res.Decision {
		case GuardDeny:
			reason := res.Reason
			if reason == "" {
				reason = fmt.Sprintf("tool %q blocked by guard rule %q", call.Name, res.RuleID)
			}
			return newErrorToolResult(call, errors.New(reason))
		case GuardAsk:
			for _, approval := range res.Approvals {
				if err := ctx.Err(); err != nil {
					return newErrorToolResult(call, err)
				}
				reply := GuardAskReply{Decision: GuardDeny}
				if e.loop.guardAsk != nil {
					var err error
					reply, err = e.loop.guardAsk(ctx, call, approval)
					if err != nil {
						return newErrorToolResult(call, fmt.Errorf("guard ask: %w", err))
					}
				}
				if reply.Decision != GuardAllow {
					reason := approval.Reason
					if reason == "" {
						reason = fmt.Sprintf("tool %q requires confirmation (rule %q)", call.Name, approval.RuleID)
					}
					if reply.Feedback != "" {
						reason = fmt.Sprintf("%s\nUser feedback: %s", reason, reply.Feedback)
					}
					return newErrorToolResult(call, errors.New(reason))
				}
			}
		}
	}
	// Cancellation while the last approval was pending must not start a tool.
	if err := ctx.Err(); err != nil {
		return newErrorToolResult(call, err)
	}

	tool, exists := e.tools[call.Name]
	if !exists {
		err := fmt.Errorf("tool %q is not available", call.Name)
		return newErrorToolResult(call, err)
	}

	if revalidate != nil {
		if err := revalidate(ctx); err != nil {
			return newErrorToolResult(call, fmt.Errorf("guard revalidation: %w", err))
		}
	}

	if err := e.checkToolVersion(ctx, call.Name); err != nil {
		return newErrorToolResult(call, err)
	}
	if err := ctx.Err(); err != nil {
		return newErrorToolResult(call, err)
	}
	var result llm.ToolResult
	var proposal []ToolReference
	var err error
	if revalidate != nil {
		ctx = context.WithValue(ctx, toolDispatchCheckKey{}, revalidate)
	}
	started = true
	if selector, ok := tool.(ToolSelector); ok {
		result, proposal, err = selector.SelectTools(ctx, call)
	} else {
		result, err = tool.Execute(ctx, call)
	}
	if err == nil && !result.IsError {
		if selectionErr := e.validateProposal(ctx, proposal); selectionErr != nil {
			return newErrorToolResult(call, selectionErr)
		}
	}
	if err != nil {
		wrapped := fmt.Errorf("tool %q failed: %w", call.Name, err)
		if result.Details == nil {
			return newErrorToolResult(call, wrapped)
		}
		// An explicit outcome may include useful partial content even when the
		// transport or operation failed. Never replace it with error text alone.
		result.CallID, result.Name, result.IsError = call.ID, call.Name, true
		result.Content = append(result.Content, llm.NewTextContent(wrapped.Error()).Part())
		message, messageErr := llm.NewToolResultMessage(result)
		if messageErr != nil {
			return unknownInvalidToolResult(call, messageErr)
		}
		return message, wrapped
	}

	result.CallID = call.ID
	result.Name = call.Name
	message, err = llm.NewToolResultMessage(result)
	if err != nil {
		if result.Details != nil {
			return unknownInvalidToolResult(call, err)
		}
		wrapped := fmt.Errorf("tool %q returned an invalid result: %w", call.Name, err)
		return newErrorToolResult(call, wrapped)
	}
	if !message.IsError {
		e.proposal = proposal
	}
	return message, nil
}

type toolDispatchCheckKey struct{}

// CheckToolDispatch lets an adapter recheck the Loop's call-local Guard permit
// after waiting for a transport queue. It neither grants permission nor retries
// execution. The capability is present only during this tool's Execute call.
func CheckToolDispatch(ctx context.Context) error {
	check, ok := ctx.Value(toolDispatchCheckKey{}).(func(context.Context) error)
	if !ok {
		return errors.New("call-local dispatch check is unavailable")
	}
	return check(ctx)
}

func unknownInvalidToolResult(call llm.ToolCall, err error) (llm.ToolResultMessage, error) {
	message, constructErr := llm.NewToolResultMessage(llm.ToolResult{
		CallID: call.ID, Name: call.Name, IsError: true,
		Content: []llm.ContentPart{llm.NewTextContent("Tool result could not be retained: " + err.Error()).Part()},
		Details: &llm.ToolResultDetails{
			State: llm.ExecutionUnknown,
			Loss:  "Invalid result payload; execution may have produced effects. Inspect current state before retrying.",
		},
	})
	return message, errors.Join(err, constructErr)
}

func (e *runExecution) syntheticToolResults(
	ctx context.Context,
	turnNumber int,
	calls []llm.ToolCall,
	reason string,
	recordHistory bool,
) error {
	for index := range calls {
		call := calls[index]
		message, err := e.notDispatchedToolResult(call, errors.New(reason))
		if err != nil {
			return err
		}
		if err := e.acceptToolResult(ctx, message, recordHistory); err != nil {
			return err
		}
		if err := e.emitToolResultMessage(ctx, turnNumber, call, message); err != nil {
			return err
		}
	}
	return nil
}

// acceptToolResult retains the actual outcome before recording or display can
// fail. The active ModelRound is the sole owner of produced tool results.
func (e *runExecution) acceptToolResult(ctx context.Context, message llm.ToolResultMessage, recordHistory bool) error {
	round := &e.result.ModelRounds[len(e.result.ModelRounds)-1]
	round.ToolResults = append(round.ToolResults, message)
	if recordHistory {
		e.history = append(e.history, message)
	}
	return e.recordMessage(ctx, message)
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

func (e *runExecution) notDispatchedDetails(name string) *llm.ToolResultDetails {
	bound, ok := e.tools[name].(BoundTool)
	if !ok {
		return nil
	}
	binding := bound.ToolBinding()
	details := &llm.ToolResultDetails{State: llm.ExecutionNotDispatched, Binding: &binding}
	if details.Validate() != nil {
		return &llm.ToolResultDetails{State: llm.ExecutionNotDispatched}
	}
	return details
}

func (e *runExecution) notDispatchedToolResult(call llm.ToolCall, cause error) (llm.ToolResultMessage, error) {
	message, err := newErrorToolResult(call, cause)
	if err == nil {
		message.Details = e.notDispatchedDetails(call.Name)
	}
	return message, err
}
