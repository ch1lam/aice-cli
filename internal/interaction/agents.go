package interaction

import "time"

// AgentUpdate is one step of a subagent run, delivered in order. Exactly one
// of Start, Event and End is set. A run is one child Agent Loop run: Start,
// its events, then End; a continued child starts another run under its ID.
type AgentUpdate struct {
	// ID names the child: a background child's task ID, or an ID of its own
	// for a synchronous one, since call IDs repeat across turns with some
	// providers.
	ID    string
	Start *AgentStart
	Event *Event
	End   *AgentEnd
}

// AgentStart describes a child run as it starts.
type AgentStart struct {
	// ToolCallID is the parent's agent call that started the child; a
	// continued child keeps the call that started it first.
	ToolCallID  string
	Description string
	// Type is "general" or "explore".
	Type       string
	Fork       bool
	Background bool
	Model      DisplayModel
	// Prompt is the run's input: the task, or the message that continues it.
	Prompt string
}

// AgentEnd settles a child run. Err reports a run that stopped early.
type AgentEnd struct {
	Err       error
	Duration  time.Duration
	Turns     int
	ToolCalls int
}

// Agents lets a frontend follow subagents as they run. Updates arrive from
// every child of the Session, synchronous or in the background, including
// those that outlive the run that started them. The frontend must receive
// continuously: children wait for room on the channel.
type Agents interface {
	AgentUpdates() <-chan AgentUpdate
	// StopAgent stops a background child for the person, without waiting;
	// the parent agent is told. A synchronous child stops with the response
	// that waits for it.
	StopAgent(id string) error
}
