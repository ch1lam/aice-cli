package streamcore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Partial is a content part that may still be streaming.
type Partial interface {
	// PartialContent returns the current content; ok=false excludes the part
	// from snapshots, for example a truncated tool call.
	PartialContent() (llm.ContentPart, bool)
}

// PartRegistry assembles ordered content snapshots from completed and
// in-progress parts.
type PartRegistry struct {
	contents map[int]llm.ContentPart
	partials map[int]Partial
}

// NewPartRegistry constructs an empty part registry.
func NewPartRegistry() *PartRegistry {
	return &PartRegistry{
		contents: make(map[int]llm.ContentPart),
		partials: make(map[int]Partial),
	}
}

// Complete records a finished content part at an index.
func (r *PartRegistry) Complete(index int, content llm.ContentPart) {
	r.contents[index] = content
	delete(r.partials, index)
}

// Partial registers an in-progress content part at an index.
func (r *PartRegistry) Partial(index int, part Partial) {
	r.partials[index] = part
}

// Has reports whether an index holds a completed or in-progress part.
func (r *PartRegistry) Has(index int) bool {
	if _, ok := r.contents[index]; ok {
		return true
	}
	_, ok := r.partials[index]
	return ok
}

// Snapshot returns the assembled content parts ordered by index.
func (r *PartRegistry) Snapshot() []llm.ContentPart {
	contents := make(map[int]llm.ContentPart, len(r.contents)+len(r.partials))
	for index, content := range r.contents {
		contents[index] = content
	}
	for index, part := range r.partials {
		if content, ok := part.PartialContent(); ok {
			contents[index] = content
		}
	}

	indexes := make([]int, 0, len(contents))
	for index := range contents {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	result := make([]llm.ContentPart, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, contents[index])
	}
	return result
}

// PartialText builds the snapshot of in-progress visible text.
func PartialText(text, signature string) llm.ContentPart {
	content := llm.NewTextContent(text).Part()
	content.Signature = signature
	return content
}

// PartialThinking builds the snapshot of in-progress thinking content.
func PartialThinking(text, signature string, redacted bool) llm.ContentPart {
	thinking := llm.NewThinkingContent(text, signature)
	thinking.Redacted = redacted
	return thinking.Part()
}

// PartialToolCall builds the snapshot of an in-progress tool call. An empty
// payload is normalized to an empty object so a zero-argument call still
// produces valid JSON; ok=false means the accumulated arguments are invalid.
func PartialToolCall(call llm.ToolCall, streamed string, initial []byte) (llm.ContentPart, bool) {
	call.Arguments = ToolCallArguments(streamed, initial)
	if !json.Valid(call.Arguments) {
		return llm.ContentPart{}, false
	}
	return llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &call}, true
}

// ToolCallArguments returns the assembled arguments for a streamed tool call,
// preferring the streamed payload and normalizing an absent payload to an
// empty object.
func ToolCallArguments(streamed string, initial []byte) json.RawMessage {
	if streamed != "" {
		return json.RawMessage(streamed)
	}
	if len(initial) > 0 {
		return append(json.RawMessage(nil), initial...)
	}
	return json.RawMessage("{}")
}

// FinishToolCall assembles and validates a completed tool call.
func FinishToolCall(call llm.ToolCall, streamed string, initial []byte) (llm.ToolCall, error) {
	call.Arguments = ToolCallArguments(streamed, initial)
	if !json.Valid(call.Arguments) {
		return call, fmt.Errorf("tool call %q ended with invalid JSON", call.Name)
	}
	return call, nil
}

// ProjectThinkingToText converts thinking parts into visible text for models
// that cannot replay opaque reasoning, dropping redacted and empty thinking.
func ProjectThinkingToText(content []llm.ContentPart) []llm.ContentPart {
	result := make([]llm.ContentPart, 0, len(content))
	for _, part := range content {
		if part.Type != llm.ContentTypeThinking {
			result = append(result, part)
			continue
		}
		if part.Redacted || strings.TrimSpace(part.Text) == "" {
			continue
		}
		result = append(result, llm.NewTextContent(part.Text).Part())
	}
	return result
}
