package interaction

import "context"

// RunInput contains one initial prompt for an interactive Agent run.
type RunInput struct {
	Prompt string
}

// ActiveRun is one prepared Agent run. Its implementation owns accepted input
// and decides when steering and follow-ups enter the Agent Loop.
type ActiveRun interface {
	Run(ctx context.Context) error
	Deliver(delivery Delivery) error
}

// Runner prepares one prompt and its ordered frontend-event bridge.
type Runner interface {
	NewRun(input RunInput, sink EventSink) (ActiveRun, error)
}

// DisplayModel identifies the model shown by an interactive frontend.
type DisplayModel struct {
	ID string
}

// DisplayThinking is the reasoning level shown by an interactive frontend.
// The empty value represents the provider default.
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

// DisplayUsage is one flattened usage snapshot for an interactive frontend.
type DisplayUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalCost        float64
}

// RuntimeState contains request settings and Session snapshots refreshed after
// an application command or Agent run.
type RuntimeState struct {
	Model            DisplayModel
	Thinking         DisplayThinking
	APIKeyConfigured bool
	Usage            DisplayUsage
	// SessionChanged reports that the active Session branch changed, so a
	// frontend must discard its visible branch transcript.
	SessionChanged bool
}

// RuntimeStateProvider reports settings changed by an application command.
type RuntimeStateProvider interface {
	RuntimeState() RuntimeState
}

// EventKind identifies one translated Agent lifecycle event.
type EventKind uint8

const (
	EventUnknown EventKind = iota
	EventAssistantStart
	EventAssistantDelta
	EventAssistantEnd
	EventToolStart
	EventToolEnd
	EventSteer
	EventFollowUp
	EventRetryStart
	EventRetryEnd
	EventAgentEnd
)

// DeltaKind identifies one streamed assistant content update.
type DeltaKind uint8

const (
	DeltaUnknown DeltaKind = iota
	DeltaText
	DeltaThinking
	DeltaToolCall
)

// Delta is one streamed assistant content update.
type Delta struct {
	Kind  DeltaKind
	Delta string
}

// AssistantDisplay is the complete assistant output of one model turn.
type AssistantDisplay struct {
	Text      string
	Thinking  string
	Concludes bool
}

// ToolDisplay is one tool execution shown by a frontend. Detail is the raw
// tool input already extracted for display by the application bridge.
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

// InputDisplay identifies one queued user input accepted by the Agent Loop.
type InputDisplay struct {
	ID   string
	Text string
}

// Event is one Agent lifecycle event translated for interactive frontends.
// Fields are populated according to Kind.
type Event struct {
	Kind      EventKind
	Delta     Delta
	Assistant AssistantDisplay
	Tool      ToolDisplay
	Input     InputDisplay
	Retry     RetryDisplay
	Err       error
}

// EventSink receives translated lifecycle events in order. Returning an error
// stops the Agent run immediately.
type EventSink func(ctx context.Context, event Event) error

// Command describes one application command exposed by an interactive
// frontend.
type Command struct {
	Name         string
	Description  string
	ArgumentHint string
	SecretPrompt string
	Menu         *CommandMenu
}

// CommandMenu describes one interactive choice level.
type CommandMenu struct {
	Title   string
	Options []CommandOption
}

// CommandOption is one selectable value. A nested menu creates another choice
// level; a leaf supplies the final arguments to the command runner.
type CommandOption struct {
	Label       string
	Description string
	Arguments   string
	Current     bool
	Menu        *CommandMenu
}

// CommandRequest is one parsed interactive command invocation.
type CommandRequest struct {
	Name      string
	Arguments string
	Secret    string
}

// CommandRunner executes application-owned interactive commands.
type CommandRunner interface {
	SlashCommands() []Command
	RunSlashCommand(ctx context.Context, request CommandRequest) (string, error)
}
