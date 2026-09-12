package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func translateAgentEvent(event agent.AgentEvent) *interaction.Event {
	switch event.Type {
	case agent.EventTypeMessageStart:
		if _, ok := event.Message.(llm.AssistantMessage); ok {
			return &interaction.Event{Kind: interaction.EventAssistantStart}
		}
	case agent.EventTypeMessageUpdate:
		return translateAssistantDelta(event.AssistantMessageEvent)
	case agent.EventTypeMessageEnd:
		if event.InputID != "" {
			input, ok := event.Message.(llm.UserMessage)
			if !ok {
				return nil
			}
			kind := interaction.EventUnknown
			switch event.InputKind {
			case agent.InputKindSteering:
				kind = interaction.EventSteer
			case agent.InputKindFollowUp:
				kind = interaction.EventFollowUp
			default:
				return nil
			}
			return &interaction.Event{
				Kind: kind,
				Input: interaction.InputDisplay{
					ID:   event.InputID,
					Text: userContent(input),
				},
			}
		}
		assistant, ok := event.Message.(llm.AssistantMessage)
		if !ok {
			return nil
		}
		text, thinking := assistantContent(assistant)
		return &interaction.Event{
			Kind: interaction.EventAssistantEnd,
			Assistant: interaction.AssistantDisplay{
				Text:      text,
				Thinking:  thinking,
				Concludes: assistantConcludes(assistant),
			},
		}
	case agent.EventTypeToolExecutionStart:
		if event.ToolCall != nil {
			return &interaction.Event{
				Kind: interaction.EventToolStart,
				Tool: displayToolCall(*event.ToolCall),
			}
		}
	case agent.EventTypeToolExecutionEnd:
		if event.ToolCall != nil {
			return &interaction.Event{
				Kind: interaction.EventToolEnd,
				Tool: interaction.ToolDisplay{
					Output:     displayToolOutput(event),
					ID:         event.ToolCall.ID,
					Failed:     event.Err != nil || (event.ToolResult != nil && event.ToolResult.IsError),
					Diff:       displayToolDiff(event),
					Truncation: displayToolTruncation(event.ToolResult),
				},
			}
		}
	case agent.EventTypeRetryStart:
		if event.Retry != nil {
			return &interaction.Event{
				Kind: interaction.EventRetryStart,
				Retry: interaction.RetryDisplay{
					Attempt:    event.Retry.Attempt,
					MaxRetries: event.Retry.MaxRetries,
					Delay:      event.Retry.Delay.String(),
				},
			}
		}
	case agent.EventTypeRetryEnd:
		if event.Retry != nil {
			return &interaction.Event{
				Kind: interaction.EventRetryEnd,
				Retry: interaction.RetryDisplay{
					Succeeded: event.Retry.Success,
				},
			}
		}
	case agent.EventTypeAgentEnd:
		return &interaction.Event{Kind: interaction.EventAgentEnd, Err: event.Err}
	}
	return nil
}

func userContent(message llm.UserMessage) string {
	var text strings.Builder
	for _, part := range message.Content {
		if part.Type == llm.ContentTypeText {
			text.WriteString(part.Text)
		} else if part.Type == llm.ContentTypeImage {
			text.WriteString("\n[Image]")
		}
	}
	return text.String()
}

func translateAssistantDelta(event *llm.Event) *interaction.Event {
	if event == nil {
		return nil
	}
	switch event.Type {
	case llm.EventTypeTextDelta:
		return &interaction.Event{
			Kind: interaction.EventAssistantDelta,
			Delta: interaction.Delta{
				Kind:  interaction.DeltaText,
				Delta: event.Delta,
			},
		}
	case llm.EventTypeThinkingDelta:
		return &interaction.Event{
			Kind: interaction.EventAssistantDelta,
			Delta: interaction.Delta{
				Kind:  interaction.DeltaThinking,
				Delta: event.Delta,
			},
		}
	case llm.EventTypeToolCallStart,
		llm.EventTypeToolCallDelta,
		llm.EventTypeToolCallEnd:
		return &interaction.Event{
			Kind: interaction.EventAssistantDelta,
			Delta: interaction.Delta{
				Kind:      interaction.DeltaToolCall,
				ToolIndex: event.ContentIndex,
				Tool:      streamedToolDisplay(event),
				Arguments: streamedToolArguments(event),
				ToolEnd:   event.Type == llm.EventTypeToolCallEnd && event.ToolCall != nil,
			},
		}
	}
	return nil
}

func assistantContent(message llm.AssistantMessage) (string, string) {
	var text, thinking strings.Builder
	for _, part := range message.Content {
		switch part.Type {
		case llm.ContentTypeText:
			text.WriteString(part.Text)
		case llm.ContentTypeThinking:
			thinking.WriteString(part.Text)
		}
	}
	return text.String(), thinking.String()
}

func assistantConcludes(message llm.AssistantMessage) bool {
	if message.StopReason == llm.StopReasonError ||
		message.StopReason == llm.StopReasonAborted {
		return false
	}
	for _, part := range message.Content {
		if part.Type == llm.ContentTypeToolCall {
			return false
		}
	}
	text, _ := assistantContent(message)
	return strings.TrimSpace(text) != ""
}

func toolCallDetail(call llm.ToolCall) string {
	var arguments struct {
		Command string `json:"command"`
		Name    string `json:"name"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return ""
	}
	switch call.Name {
	case "bash":
		return arguments.Command
	case "skill":
		return arguments.Name
	default:
		return arguments.Path
	}
}

func displayThinking(level llm.ThinkingLevel) (interaction.DisplayThinking, error) {
	switch level {
	case llm.ThinkingLevelUnknown:
		return interaction.DisplayThinkingDefault, nil
	case llm.ThinkingLevelOff:
		return interaction.DisplayThinkingOff, nil
	case llm.ThinkingLevelMinimal:
		return interaction.DisplayThinkingMinimal, nil
	case llm.ThinkingLevelLow:
		return interaction.DisplayThinkingLow, nil
	case llm.ThinkingLevelMedium:
		return interaction.DisplayThinkingMedium, nil
	case llm.ThinkingLevelHigh:
		return interaction.DisplayThinkingHigh, nil
	case llm.ThinkingLevelXHigh:
		return interaction.DisplayThinkingXHigh, nil
	case llm.ThinkingLevelMax:
		return interaction.DisplayThinkingMax, nil
	default:
		return interaction.DisplayThinkingDefault, fmt.Errorf(
			"app: unsupported thinking level %q",
			level,
		)
	}
}

func newDisplayUsage(usage llm.Usage) interaction.DisplayUsage {
	display := interaction.DisplayUsage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
	if usage.Cost != nil {
		display.TotalCost = usage.Cost.Total
	}
	return display
}

func displayToolTruncation(result *llm.ToolResultMessage) interaction.TruncationDisplay {
	if result == nil || result.Truncation.Reason == "" {
		return interaction.TruncationDisplay{}
	}
	t := result.Truncation
	if result.ToolName == "grep" {
		reasons := make([]string, 0, 3)
		hints := make([]string, 0, 3)
		if t.Reason == llm.TruncationByteLimit {
			reasons = append(reasons, "50 KiB limit")
		}
		if t.MatchLimitReached > 0 {
			reasons = append(reasons, fmt.Sprintf("%d matches limit", t.MatchLimitReached))
			hints = append(hints, "increase limit")
		}
		if t.Reason == llm.TruncationByteLimit || t.MatchLimitReached > 0 {
			hints = append(hints, "refine pattern or path")
		}
		if t.LinesTruncated {
			reasons = append(reasons, "some lines truncated")
			hints = append(hints, "use read for full lines")
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "output limit")
			hints = append(hints, "refine pattern or path")
		}
		return interaction.TruncationDisplay{
			Reason:      strings.Join(reasons, ", "),
			OutputLines: t.OutputLines,
			OutputBytes: t.OutputBytes,
			Hint:        strings.Join(hints, "; "),
		}
	}
	reason := "output limit"
	switch t.Reason {
	case llm.TruncationRequestedLines:
		reason = "requested line limit"
	case llm.TruncationLineLimit:
		reason = "2000 line limit"
	case llm.TruncationByteLimit:
		reason = "50 KiB limit"
	case llm.TruncationOversizedLine:
		reason = "line exceeds output budget"
	}
	return interaction.TruncationDisplay{
		Reason:          reason,
		OutputLines:     t.OutputLines,
		OutputBytes:     t.OutputBytes,
		NextOffset:      t.NextOffset,
		TotalLines:      t.TotalLines,
		TotalLinesKnown: t.TotalLinesKnown,
		RequiresBash:    t.Reason == llm.TruncationOversizedLine,
	}
}

func displayToolCall(call llm.ToolCall) interaction.ToolDisplay {
	display := interaction.ToolDisplay{ID: call.ID, Name: call.Name, Detail: toolCallDetail(call)}
	if call.Name == "write" {
		var args struct {
			Content *string `json:"content"`
		}
		if json.Unmarshal(call.Arguments, &args) == nil && args.Content != nil {
			display.Content = *args.Content
			display.HasContent = true
		}
	}
	return display
}

func streamedToolDisplay(event *llm.Event) interaction.ToolDisplay {
	if event.ToolCall != nil {
		return displayToolCall(*event.ToolCall)
	}
	if event.ToolCallDelta != nil {
		return interaction.ToolDisplay{ID: event.ToolCallDelta.ID, Name: event.ToolCallDelta.Name}
	}
	return interaction.ToolDisplay{}
}

func streamedToolArguments(event *llm.Event) string {
	if event.ToolCallDelta != nil {
		return event.ToolCallDelta.ArgumentsDelta
	}
	return ""
}

func displayToolDiff(event agent.AgentEvent) interaction.DiffDisplay {
	if event.ToolCall == nil || event.ToolCall.Name != "edit" ||
		event.Err != nil || event.ToolResult == nil || event.ToolResult.IsError {
		return interaction.DiffDisplay{}
	}
	return interaction.DiffDisplay{Text: event.ToolResult.Diff.Text, Truncated: event.ToolResult.Diff.Truncated}
}
