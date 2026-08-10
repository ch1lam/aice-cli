package app

import (
	"encoding/json"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func translateAgentEvent(event agent.AgentEvent) *tui.DisplayEvent {
	switch event.Type {
	case agent.EventTypeMessageStart:
		if _, ok := event.Message.(llm.AssistantMessage); ok {
			return &tui.DisplayEvent{Kind: tui.DisplayEventAssistantStart}
		}
	case agent.EventTypeMessageUpdate:
		return translateAssistantDelta(event.AssistantMessageEvent)
	case agent.EventTypeMessageEnd:
		if event.SteeringID != "" {
			steering, ok := event.Message.(llm.UserMessage)
			if !ok {
				return nil
			}
			return &tui.DisplayEvent{
				Kind: tui.DisplayEventSteer,
				Steering: tui.SteeringDisplay{
					ID:   event.SteeringID,
					Text: userContent(steering),
				},
			}
		}
		assistant, ok := event.Message.(llm.AssistantMessage)
		if !ok {
			return nil
		}
		text, thinking := assistantContent(assistant)
		return &tui.DisplayEvent{
			Kind: tui.DisplayEventAssistantEnd,
			Assistant: tui.AssistantDisplay{
				Text:      text,
				Thinking:  thinking,
				Concludes: assistantConcludes(assistant),
			},
		}
	case agent.EventTypeToolExecutionStart:
		if event.ToolCall != nil {
			return &tui.DisplayEvent{
				Kind: tui.DisplayEventToolStart,
				Tool: tui.ToolDisplay{
					ID:     event.ToolCall.ID,
					Name:   event.ToolCall.Name,
					Detail: toolCallDetail(*event.ToolCall),
				},
			}
		}
	case agent.EventTypeToolExecutionEnd:
		if event.ToolCall != nil {
			return &tui.DisplayEvent{
				Kind: tui.DisplayEventToolEnd,
				Tool: tui.ToolDisplay{
					ID:     event.ToolCall.ID,
					Failed: event.Err != nil,
				},
			}
		}
	case agent.EventTypeRetryStart:
		if event.Retry != nil {
			return &tui.DisplayEvent{
				Kind: tui.DisplayEventRetryStart,
				Retry: tui.RetryDisplay{
					Attempt:    event.Retry.Attempt,
					MaxRetries: event.Retry.MaxRetries,
					Delay:      event.Retry.Delay.String(),
				},
			}
		}
	case agent.EventTypeRetryEnd:
		if event.Retry != nil {
			return &tui.DisplayEvent{
				Kind: tui.DisplayEventRetryEnd,
				Retry: tui.RetryDisplay{
					Succeeded: event.Retry.Success,
				},
			}
		}
	case agent.EventTypeAgentEnd:
		return &tui.DisplayEvent{Kind: tui.DisplayEventAgentEnd, Err: event.Err}
	}
	return nil
}

func userContent(message llm.UserMessage) string {
	var text strings.Builder
	for _, part := range message.Content {
		if part.Type == llm.ContentTypeText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func translateAssistantDelta(event *llm.Event) *tui.DisplayEvent {
	if event == nil {
		return nil
	}
	switch event.Type {
	case llm.EventTypeTextDelta:
		return &tui.DisplayEvent{
			Kind: tui.DisplayEventAssistantDelta,
			Delta: tui.DisplayDelta{
				Kind:  tui.DisplayDeltaText,
				Delta: event.Delta,
			},
		}
	case llm.EventTypeThinkingDelta:
		return &tui.DisplayEvent{
			Kind: tui.DisplayEventAssistantDelta,
			Delta: tui.DisplayDelta{
				Kind:  tui.DisplayDeltaThinking,
				Delta: event.Delta,
			},
		}
	case llm.EventTypeToolCallStart,
		llm.EventTypeToolCallDelta,
		llm.EventTypeToolCallEnd:
		return &tui.DisplayEvent{
			Kind: tui.DisplayEventAssistantDelta,
			Delta: tui.DisplayDelta{
				Kind: tui.DisplayDeltaToolCall,
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
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return ""
	}
	if call.Name == "bash" {
		return arguments.Command
	}
	return arguments.Path
}

func newDisplayUsage(usage llm.Usage) tui.DisplayUsage {
	display := tui.DisplayUsage{
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
