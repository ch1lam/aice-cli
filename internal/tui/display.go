package tui

import "github.com/ch1lam/aice-cli/internal/interaction"

type DisplayModel = interaction.DisplayModel
type DisplayThinking = interaction.DisplayThinking

const (
	DisplayThinkingDefault = interaction.DisplayThinkingDefault
	DisplayThinkingOff     = interaction.DisplayThinkingOff
	DisplayThinkingMinimal = interaction.DisplayThinkingMinimal
	DisplayThinkingLow     = interaction.DisplayThinkingLow
	DisplayThinkingMedium  = interaction.DisplayThinkingMedium
	DisplayThinkingHigh    = interaction.DisplayThinkingHigh
	DisplayThinkingXHigh   = interaction.DisplayThinkingXHigh
	DisplayThinkingMax     = interaction.DisplayThinkingMax
)

type DisplayUsage = interaction.DisplayUsage
type DisplayEventKind = interaction.EventKind

const (
	DisplayEventUnknown        = interaction.EventUnknown
	DisplayEventAssistantStart = interaction.EventAssistantStart
	DisplayEventAssistantDelta = interaction.EventAssistantDelta
	DisplayEventAssistantEnd   = interaction.EventAssistantEnd
	DisplayEventToolStart      = interaction.EventToolStart
	DisplayEventToolEnd        = interaction.EventToolEnd
	DisplayEventSteer          = interaction.EventSteer
	DisplayEventFollowUp       = interaction.EventFollowUp
	DisplayEventRetryStart     = interaction.EventRetryStart
	DisplayEventRetryEnd       = interaction.EventRetryEnd
	DisplayEventAgentEnd       = interaction.EventAgentEnd
)

type DisplayDeltaKind = interaction.DeltaKind

const (
	DisplayDeltaUnknown  = interaction.DeltaUnknown
	DisplayDeltaText     = interaction.DeltaText
	DisplayDeltaThinking = interaction.DeltaThinking
	DisplayDeltaToolCall = interaction.DeltaToolCall
)

type DisplayDelta = interaction.Delta
type AssistantDisplay = interaction.AssistantDisplay
type ToolDisplay = interaction.ToolDisplay
type RetryDisplay = interaction.RetryDisplay
type InputDisplay = interaction.InputDisplay
type DisplayEvent = interaction.Event
type DisplayEventSink = interaction.EventSink
