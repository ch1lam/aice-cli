package interaction

import (
	"context"
	"errors"
	"time"
)

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

// SideThreadStatus is the lifecycle state of one ephemeral /btw thread.
type SideThreadStatus uint8

const (
	// SideThreadUnknown is the unset state and is never returned by a live
	// registry.
	SideThreadUnknown SideThreadStatus = iota
	// SideThreadRunning means an answer is in flight.
	SideThreadRunning
	// SideThreadWritable means a follow-up question may still be asked.
	SideThreadWritable
	// SideThreadReadOnly means the follow-up window closed but the thread
	// remains viewable until it expires.
	SideThreadReadOnly
)

// SideThread is a defensive metadata snapshot of one ephemeral side thread.
// Values are copied at snapshot time; the registry owns the live state.
type SideThread struct {
	ID    uint64
	Title string
	// Status is computed against the registry clock at snapshot time.
	Status SideThreadStatus
	// LastActiveAt is when the thread's most recent answer terminated, or
	// when the thread was created before its first answer.
	LastActiveAt time.Time
}

// Side-thread lifecycle failures. These are expected conditions, so
// callers must match them with errors.Is rather than string comparison.
var (
	// ErrSideThreadNotFound indicates the id does not exist: never created,
	// already closed, or expired.
	ErrSideThreadNotFound = errors.New("side thread not found")
	// ErrSideThreadBusy indicates the thread's previous answer is still
	// running.
	ErrSideThreadBusy = errors.New("side thread is already answering")
	// ErrSideThreadReadOnly indicates the thread's follow-up window closed.
	ErrSideThreadReadOnly = errors.New("side thread is read-only")
	// ErrSideThreadLimit indicates the maximum number of live threads.
	ErrSideThreadLimit = errors.New("side thread limit reached")
	// ErrSideThreadConcurrencyLimit indicates the maximum number of
	// simultaneously answering threads is already active.
	ErrSideThreadConcurrencyLimit = errors.New("too many side answers running")
	// ErrSideThreadRunning indicates the thread cannot be closed while an
	// answer is in flight.
	ErrSideThreadRunning = errors.New("side thread is still answering")
)

// SideThreadManager owns the in-memory registry of ephemeral /btw threads.
// The registry is the single authority for thread lifetimes, idle windows,
// expiry, and limits; frontends only render its snapshots and ask it to
// create, open, or close threads.
type SideThreadManager interface {
	// SideThreads lists every live thread, most recently active first.
	// Expired threads are permanently deleted as part of the listing. The
	// returned slice and entries are defensive copies.
	SideThreads() []SideThread
	// CreateSideThread creates a new thread from its first question: it
	// freezes the accepted parent context at this moment and returns a
	// Runner prepared for the first interaction. The runner gates its
	// actual start against run-start limits through the registry.
	CreateSideThread(prompt string) (SideThread, Runner, error)
	// OpenSideThread looks up a live thread and returns its metadata plus
	// the Runner used for follow-up interactions. Run-start validation
	// (read-only, expiry, concurrency) still happens when the runner
	// actually starts, so a frontend cannot bypass it by holding a runner.
	OpenSideThread(id uint64) (SideThread, Runner, error)
	// CloseSideThread permanently deletes a thread. A thread with an answer
	// in flight is refused with ErrSideThreadRunning.
	CloseSideThread(id uint64) error
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
	// UseSavedCredential executes the selected command without prompting for a
	// replacement secret. It is intended for menu actions that switch to an
	// already configured provider.
	UseSavedCredential bool
	Menu               *CommandMenu
}

// CommandRequest is one parsed interactive command invocation.
type CommandRequest struct {
	Name               string
	Arguments          string
	Secret             string
	UseSavedCredential bool
}

// CommandRunner executes application-owned interactive commands.
type CommandRunner interface {
	SlashCommands() []Command
	RunSlashCommand(ctx context.Context, request CommandRequest) (string, error)
}

// GuardOption is one selectable choice in a guard confirmation prompt.
type GuardOption struct {
	ID     string // stable identifier assigned by the requester
	Label  string // self-contained, user-facing scope description
	Detail string // optional secondary line, may be empty
	Deny   bool   // true when choosing this option denies the action
}

// GuardReply is the user's answer to one GuardRequest.
type GuardReply struct {
	OptionID string
	Feedback string // optional note to the model; meaningful only for deny options
}

// GuardRequest is one interactive guard confirmation sent from the agent loop
// to the frontend. The frontend must send one GuardReply on Reply.
type GuardRequest struct {
	ID        string
	ToolName  string
	Reason    string
	RuleID    string
	Command   string
	Path      string
	Highlight string
	Options   []GuardOption
	Reply     chan GuardReply
}

// GuardRequester exposes the channel of pending guard confirmations for the
// interactive TUI to consume.
type GuardRequester interface {
	GuardRequests() <-chan GuardRequest
}
