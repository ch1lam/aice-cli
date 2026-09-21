package llm

import (
	"fmt"
	"math"
	"slices"
)

// CloneAgentMessages deep-copies a transcript slice so neither side can
// mutate the other's message values. Unknown message types fail closed.
func CloneAgentMessages(messages []AgentMessage) ([]AgentMessage, error) {
	if messages == nil {
		return nil, nil
	}
	cloned := make([]AgentMessage, len(messages))
	for index, message := range messages {
		copied, err := CloneAgentMessage(message)
		if err != nil {
			return nil, fmt.Errorf("llm: clone agent message %d: %w", index, err)
		}
		cloned[index] = copied
	}
	return cloned, nil
}

// CloneAgentMessage validates and deep-copies one transcript message without
// JSON encoding. Immutable strings are shared; mutable payloads are independent.
func CloneAgentMessage(message AgentMessage) (AgentMessage, error) {
	if err := validateAgentMessage(message); err != nil {
		return nil, err
	}

	switch value := message.(type) {
	case UserMessage:
		copied := value
		copied.Content = cloneContentParts(value.Content)
		return copied, nil
	case AssistantMessage:
		copied := value
		copied.Content = cloneContentParts(value.Content)
		if value.Usage.Cost != nil {
			for _, amount := range [...]float64{
				value.Usage.Cost.Input, value.Usage.Cost.Output,
				value.Usage.Cost.CacheRead, value.Usage.Cost.CacheWrite, value.Usage.Cost.Total,
			} {
				if math.IsNaN(amount) || math.IsInf(amount, 0) {
					return nil, fmt.Errorf("llm: usage cost must be finite")
				}
			}
			cost := *value.Usage.Cost
			copied.Usage.Cost = &cost
		}
		return copied, nil
	case ToolResultMessage:
		copied := value
		copied.Content = cloneContentParts(value.Content)
		copied.Evidence = value.Evidence.Clone()
		return copied, nil
	case CompactionSummaryMessage:
		return value, nil
	case nil:
		return nil, fmt.Errorf("llm: clone agent message: message is nil")
	default:
		return nil, fmt.Errorf("llm: clone agent message: unsupported type %T", message)
	}
}

// cloneContentParts deep-copies one message's content slice, including image
// bytes, tool-call raw arguments, and recursively nested tool-result content,
// so no mutable field can alias between a frozen snapshot and the parent.
func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]ContentPart, len(parts))
	for index, part := range parts {
		cloned[index] = part
		switch part.Type {
		case ContentTypeImage:
			if part.Image != nil {
				image := part.Image.Clone()
				cloned[index].Image = &image
			}
		case ContentTypeToolCall:
			if part.ToolCall != nil {
				call := *part.ToolCall
				call.Arguments = slices.Clone(part.ToolCall.Arguments)
				cloned[index].ToolCall = &call
			}
		case ContentTypeToolResult:
			if part.ToolResult != nil {
				result := *part.ToolResult
				result.Content = cloneContentParts(part.ToolResult.Content)
				result.Evidence = part.ToolResult.Evidence.Clone()
				cloned[index].ToolResult = &result
			}
		}
	}
	return cloned
}
