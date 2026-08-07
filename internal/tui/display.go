package tui

import "context"

// DisplayModel identifies the model shown in the status line.
type DisplayModel struct {
	ID string
}

// DisplayThinking is the reasoning level shown in the status line. The empty
// value represents the provider default.
type DisplayThinking string

const (
	DisplayThinkingDefault DisplayThinking = ""
	DisplayThinkingOff     DisplayThinking = "off"
	DisplayThinkingMinimal DisplayThinking = "minimal"
	DisplayThinkingLow     DisplayThinking = "low"
	DisplayThinkingMedium  DisplayThinking = "medium"
	DisplayThinkingHigh    DisplayThinking = "high"
	DisplayThinkingXHigh   DisplayThinking = "xhigh"
	DisplayThinkingMax     DisplayThinking = "max"
)

// DisplayUsage is one flattened usage snapshot shown in the status line.
type DisplayUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalCost        float64
}

// DisplayEventKind identifies one translated agent lifecycle event.
type DisplayEventKind uint8

const (
	DisplayEventUnknown DisplayEventKind = iota
	DisplayEventAssistantStart
	DisplayEventAssistantDelta
	DisplayEventAssistantEnd
	DisplayEventToolStart
	DisplayEventToolEnd
	DisplayEventRetryStart
	DisplayEventRetryEnd
	DisplayEventAgentEnd
)

// DisplayDeltaKind identifies one streamed assistant content update.
type DisplayDeltaKind uint8

const (
	DisplayDeltaUnknown DisplayDeltaKind = iota
	DisplayDeltaText
	DisplayDeltaThinking
	DisplayDeltaToolCall
)

// DisplayDelta is one streamed assistant content update.
type DisplayDelta struct {
	Kind  DisplayDeltaKind
	Delta string
}

// AssistantDisplay is the complete assistant output of one model turn.
type AssistantDisplay struct {
	Text      string
	Thinking  string
	Concludes bool
}

// ToolDisplay is one tool execution shown in the transcript. Detail is the
// raw tool input already extracted for display by the application bridge.
type ToolDisplay struct {
	ID     string
	Name   string
	Detail string
	Failed bool
}

// RetryDisplay is one model-call retry status. Delay is pre-formatted by the
// application bridge.
type RetryDisplay struct {
	Attempt    int
	MaxRetries int
	Delay      string
	Succeeded  bool
}

// DisplayEvent is one agent lifecycle event translated for the interface.
// Fields are populated according to Kind.
type DisplayEvent struct {
	Kind      DisplayEventKind
	Delta     DisplayDelta
	Assistant AssistantDisplay
	Tool      ToolDisplay
	Retry     RetryDisplay
	Err       error
}

// DisplayEventSink receives translated lifecycle events in order. Returning
// an error stops the agent run immediately.
type DisplayEventSink func(ctx context.Context, event DisplayEvent) error
