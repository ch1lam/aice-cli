package tui

import (
	"context"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// runKind is the controller work the main view is waiting on.
type runKind uint8

const (
	runIdle    runKind = iota
	runPrompt          // a composer prompt; deliveries go to its ActiveRun
	runCommand         // an application slash command, login or credential save
	runRestore         // the session picker resuming a Session
)

// runState is the main controller work in progress. It is a value: Bubble Tea
// copies the model and the history reader keeps a previous copy, so a pointer
// here would share run state between them. finishRun resets it as a whole.
type runState struct {
	kind runKind
	// active is set once the controller accepts a prompt run; steering and
	// follow-ups need it.
	active interaction.ActiveRun
	// cancel arrives after the start. cancelRequested covers the interval
	// before it and outlasts later stream updates until the run finishes.
	cancel          context.CancelFunc
	cancelRequested bool
	// submitted is retained until the controller accepts it, so a rejected
	// start restores the composer.
	submitted      *interaction.RunInput
	submittedDraft composerDraft
}

func (r runState) running() bool { return r.kind != runIdle }

// acceptsDelivery reports whether steering and follow-ups can reach the run.
func (r runState) acceptsDelivery() bool { return r.active != nil }

func (m model) restoringSession() bool { return m.run.kind == runRestore }
