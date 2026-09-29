package llm

import (
	"encoding/json"
	"unicode/utf8"
)

// ResultViewBudget bounds each bound result's model view. Storage retains the
// source independently. Unknown windows use the same 4k-token maximum; small
// windows reserve at least 256 estimated tokens for state and readback guidance.
func ResultViewBudget(contextWindow int64) int64 {
	if contextWindow <= 0 {
		return 4096
	}
	return max(256, min(4096, contextWindow/10))
}

const structuredPreviewLabel = "Structured result preview (raw UTF-8 prefix; incomplete, not a complete JSON document):\n"

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
// altering durable history. Structured JSON stays whole in Details; an oversized
// value gets an explicitly incomplete raw text preview instead. Only results
// carrying source details are affected; legacy/local tools keep their contracts.
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
	notice := resultReadbackNotice(source, tokens/2)
	remaining := max(int64(0), tokens-EstimateMessageTokens(view)-EstimateTextTokens(notice))
	structured := source.Details.StructuredContent
	// Reserve a modest share before ordered content consumes the budget. This
	// keeps structured-only facts visible without displacing all text/images.
	structuredBudget := int64(0)
	if len(structured) > 0 {
		structuredBudget = min(512, remaining/4)
		if len(source.Content) == 0 {
			structuredBudget = remaining
		}
	}
	remaining -= structuredBudget
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
	remaining += structuredBudget
	if len(structured) > 0 && EstimateTextTokens("Structured result (JSON):\n"+string(structured)) <= remaining {
		view.Details.StructuredContent = append([]byte(nil), structured...)
	} else if len(structured) > 0 {
		prefix := resultTextPrefix(string(structured), max(0, remaining-EstimateTextTokens(structuredPreviewLabel)))
		if prefix != "" {
			view.Content = append(view.Content, NewTextContent(structuredPreviewLabel+prefix).Part())
		}
	}
	view.Content = append(view.Content, NewTextContent(notice).Part())
	return view
}

func resultReadbackNotice(source ToolResultMessage, tokens int64) string {
	section := "metadata"
	if len(source.Details.StructuredContent) > 0 {
		section = "structured"
	}
	args, _ := json.Marshal(struct {
		CallID  string `json:"call_id"`
		Section string `json:"section"`
	}{source.ToolCallID, section})
	notice := "Model view trimmed. Read retained source with tool_result_read arguments:\n" + string(args) +
		"\nUse section=metadata for block indices; section=content with block reads text/images. Storage loss cannot be recovered."
	if source.ToolCallID != "" && len(source.ToolCallID) <= 4096 && EstimateTextTokens(notice) <= tokens {
		return notice
	}
	// Never offer a truncated or invented selector when an unusual ID would
	// consume the view. The message envelope still carries the exact identity.
	return "Model view trimmed. Use tool_result_read with the exact call_id from this result's tool-call envelope; section=metadata lists blocks, section=structured reads JSON. The selector could not fit here. Storage loss cannot be recovered."
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
