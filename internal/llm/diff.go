package llm

// ToolDiff is a bounded, tool-produced view of a completed mutation. It is
// Session metadata, never model-facing content. Zero means no diff is available.
// Text uses unified hunks without file headers; Truncated marks omitted output.
// Strings keep the value immutable across recorder and display boundaries.
type ToolDiff struct {
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
