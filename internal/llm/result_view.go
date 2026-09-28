package llm

import "unicode/utf8"

// ResultViewBudget bounds each bound result's model view. Storage retains the
// source independently. Unknown windows use the same 4k-token maximum; small
// windows reserve at least 256 estimated tokens for state and readback guidance.
func ResultViewBudget(contextWindow int64) int64 {
	if contextWindow <= 0 {
		return 4096
	}
	return max(256, min(4096, contextWindow/10))
}

const resultViewNotice = "Model view trimmed. Retained source can be read with tool_result_read using this call_id (metadata, content block index, or structured section). Storage loss is separate; missing source data cannot be recovered."

// BoundToolResultViews updates an already projected, caller-owned message slice.
// The return value signals that old provider usage may describe a different
// result view, so callers should estimate the current request instead.
func BoundToolResultViews(messages []Message, tokens int64) bool {
	bounded := false
	if tokens <= 0 {
		return false
	}
	for i, message := range messages {
		if result, ok := message.(ToolResultMessage); ok && result.Details != nil {
			messages[i] = BoundToolResultView(result, tokens)
			bounded = true
		}
	}
	return bounded
}

// BoundToolResultView preserves block order and execution/loss metadata without
// altering durable history. Whole structured JSON is retained or omitted; it
// is never truncated into invalid JSON. Only results carrying source details
// are affected; legacy/local tools keep their existing presentation contracts.
func BoundToolResultView(source ToolResultMessage, tokens int64) ToolResultMessage {
	oversizedImage := false
	for _, part := range source.Content {
		oversizedImage = oversizedImage || part.Image != nil && len(part.Image.Data) > 4<<20
	}
	if tokens <= 0 || source.Details == nil || !oversizedImage && EstimateMessageTokens(source) <= max(tokens, 256) {
		return source
	}
	tokens = max(tokens, 256)
	view := source
	view.Content = nil
	view.Details = source.Details.Clone()
	view.Details.StructuredContent = nil
	if EstimateTextTokens(view.Details.Loss) > tokens/4 {
		view.Details.Loss = "Source data was omitted; use tool_result_read metadata for the recorded loss details."
	}
	remaining := max(int64(0), tokens-EstimateMessageTokens(view)-EstimateTextTokens(resultViewNotice))
	for _, part := range source.Content {
		switch part.Type {
		case ContentTypeText:
			text := resultTextPrefix(part.Text, remaining)
			if text != "" {
				view.Content = append(view.Content, NewTextContent(text).Part())
				remaining -= EstimateTextTokens(text)
			}
		case ContentTypeImage:
			cost := estimatedImageUnits / 4
			if remaining >= cost && part.Image != nil && len(part.Image.Data) <= 4<<20 {
				image := part.Image.Clone()
				view.Content = append(view.Content, ContentPart{Type: ContentTypeImage, Image: &image})
				remaining -= cost
			}
		}
	}
	structured := source.Details.StructuredContent
	if len(structured) > 0 && EstimateTextTokens("Structured result (JSON):\n"+string(structured)) <= remaining {
		view.Details.StructuredContent = append([]byte(nil), structured...)
	}
	view.Content = append(view.Content, NewTextContent(resultViewNotice).Part())
	return view
}

func resultTextPrefix(text string, tokens int64) string {
	units, end := int64(0), 0
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		cost := int64(1)
		if r > 127 {
			cost = 4
		}
		if units+cost > tokens*4 {
			break
		}
		units += cost
		end += size
	}
	return text[:end]
}
