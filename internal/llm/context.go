package llm

import "unicode/utf8"

const estimatedImageUnits int64 = 4_800

// ContextUsageEstimate explains how an estimated context size was derived.
type ContextUsageEstimate struct {
	Tokens         int64
	UsageTokens    int64
	TrailingTokens int64
	LastUsageIndex int
}

// ContextTokens returns the provider-reported context size represented by usage.
func ContextTokens(usage Usage) int64 {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return positive(usage.InputTokens) +
		positive(usage.OutputTokens) +
		positive(usage.CacheReadTokens) +
		positive(usage.CacheWriteTokens)
}

// EstimateContextTokens prefers the last applicable provider usage block and
// estimates only messages added after it. Without usage, it estimates the full
// request context, including the system prompt and tool definitions.
func EstimateContextTokens(request Request) ContextUsageEstimate {
	usage, usageIndex, ok := lastApplicableUsage(request.Messages)
	if ok {
		usageTokens := ContextTokens(usage)
		var trailingTokens int64
		for index := usageIndex + 1; index < len(request.Messages); index++ {
			trailingTokens += EstimateMessageTokens(request.Messages[index])
		}
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailingTokens,
			UsageTokens:    usageTokens,
			TrailingTokens: trailingTokens,
			LastUsageIndex: usageIndex,
		}
	}

	tokens := EstimateTextTokens(request.SystemPrompt)
	for _, tool := range request.Tools {
		tokens += estimateToolTokens(tool)
	}
	for _, message := range request.Messages {
		tokens += EstimateMessageTokens(message)
	}
	return ContextUsageEstimate{
		Tokens:         tokens,
		TrailingTokens: tokens,
		LastUsageIndex: -1,
	}
}

// EstimateTextTokens conservatively estimates tokens without provider-specific
// tokenizers. ASCII text uses four characters per token; non-ASCII runes count
// as one token each so CJK prompts are not severely underestimated.
func EstimateTextTokens(text string) int64 {
	var units int64
	for len(text) > 0 {
		value, size := utf8.DecodeRuneInString(text)
		if value == utf8.RuneError && size == 1 {
			units += 4
			text = text[1:]
			continue
		}
		if value <= 0x7f {
			units++
		} else {
			units += 4
		}
		text = text[size:]
	}
	return divideRoundUp(units, 4)
}

// EstimateMessageTokens estimates one standard LLM message.
func EstimateMessageTokens(message Message) int64 {
	switch value := message.(type) {
	case UserMessage:
		return estimateContentTokens(value.Content)
	case AssistantMessage:
		return estimateContentTokens(value.Content)
	case ToolResultMessage:
		return estimateContentTokens(value.Content)
	default:
		return 0
	}
}

func lastApplicableUsage(messages []Message) (Usage, int, bool) {
	latestPrefixTimestamp := int64(-1 << 63)
	var usage Usage
	usageIndex := -1
	for index, message := range messages {
		timestamp := messageTimestamp(message)
		if assistant, ok := message.(AssistantMessage); ok &&
			assistant.Timestamp >= latestPrefixTimestamp &&
			assistant.StopReason != StopReasonAborted &&
			assistant.StopReason != StopReasonError &&
			ContextTokens(assistant.Usage) > 0 {
			usage = assistant.Usage
			usageIndex = index
		}
		if timestamp > latestPrefixTimestamp {
			latestPrefixTimestamp = timestamp
		}
	}
	return usage, usageIndex, usageIndex >= 0
}

func messageTimestamp(message Message) int64 {
	switch value := message.(type) {
	case UserMessage:
		return value.Timestamp
	case AssistantMessage:
		return value.Timestamp
	case ToolResultMessage:
		return value.Timestamp
	default:
		return 0
	}
}

func estimateContentTokens(content []ContentPart) int64 {
	var tokens int64
	for _, part := range content {
		switch part.Type {
		case ContentTypeText, ContentTypeThinking:
			tokens += EstimateTextTokens(part.Text)
		case ContentTypeImage:
			tokens += divideRoundUp(estimatedImageUnits, 4)
		case ContentTypeToolCall:
			if part.ToolCall != nil {
				tokens += EstimateTextTokens(part.ToolCall.Name)
				tokens += EstimateTextTokens(string(part.ToolCall.Arguments))
			}
		case ContentTypeToolResult:
			if part.ToolResult != nil {
				tokens += EstimateTextTokens(part.ToolResult.Name)
				tokens += estimateContentTokens(part.ToolResult.Content)
			}
		}
	}
	return tokens
}

func estimateToolTokens(tool ToolDefinition) int64 {
	return EstimateTextTokens(tool.Name) +
		EstimateTextTokens(tool.Description) +
		EstimateTextTokens(string(tool.InputSchema))
}

func divideRoundUp(value, divisor int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func positive(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
