package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type interactiveRun struct {
	session *interactiveSession
	prompt  llm.UserMessage
	sink    interaction.EventSink
	mailbox *interaction.Mailbox

	mu        sync.Mutex
	isStarted bool
}

type mainRunState struct {
	pendingMessages []llm.AgentMessage
}

type mainRunSnapshot struct {
	state        *mainRunState
	loop         *agent.Loop
	history      []llm.AgentMessage
	model        llm.Model
	options      llm.StreamOptions
	systemPrompt string
}

var _ interaction.ActiveRun = (*interactiveRun)(nil)

func (s *interactiveSession) NewRun(
	input interaction.RunInput,
	sink interaction.EventSink,
) (interaction.ActiveRun, error) {
	if s == nil {
		return nil, fmt.Errorf("app: interactive Session is required")
	}
	settings := s.settingsSnapshot()
	if settings.loop == nil {
		return nil, credentialNotConfiguredError(
			s.providers,
			settings.configuration,
		)
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent(input.Prompt).Part())
	if err != nil {
		return nil, fmt.Errorf("app: create prompt: %w", err)
	}
	return &interactiveRun{
		session: s,
		prompt:  prompt,
		sink:    sink,
		mailbox: interaction.NewMailbox(),
	}, nil
}

func (r *interactiveRun) Deliver(delivery interaction.Delivery) error {
	if r == nil || r.mailbox == nil {
		return interaction.ErrClosed
	}
	return r.mailbox.Deliver(delivery)
}

func (r *interactiveRun) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if r == nil || r.session == nil || r.mailbox == nil {
		return fmt.Errorf("app: interactive run is not initialized")
	}
	r.mu.Lock()
	if r.isStarted {
		r.mu.Unlock()
		return fmt.Errorf("app: interactive run already started")
	}
	r.isStarted = true
	r.mu.Unlock()
	defer r.mailbox.Seal()

	snapshot, err := r.session.beginMainRun(r.prompt)
	if err != nil {
		return err
	}
	defer r.session.endMainRun(snapshot.state)

	persistedMessages := 0
	result, runErr := snapshot.loop.Run(ctx, agent.RunInput{
		Model:        snapshot.model,
		SystemPrompt: snapshot.systemPrompt,
		History:      snapshot.history,
		Prompt:       r.prompt,
		Options:      snapshot.options,
		Steering:     mailboxInputSource(r.mailbox.TakeSteering, "steering"),
		FollowUp:     mailboxInputSource(r.mailbox.TakeFollowUp, "follow-up"),
	}, func(eventCtx context.Context, event agent.AgentEvent) error {
		switch {
		case event.Type == agent.EventTypeMessageStart && event.InputID != "":
			input, ok := event.Message.(llm.UserMessage)
			if !ok {
				return fmt.Errorf(
					"app: active main input has type %T, want llm.UserMessage",
					event.Message,
				)
			}
			if err := r.session.registerMainMessages(
				snapshot.state,
				[]llm.AgentMessage{input},
			); err != nil {
				return err
			}
		case event.Type == agent.EventTypeTurnEnd && event.Message != nil:
			messages := make(
				[]llm.AgentMessage,
				0,
				1+len(event.ToolResults),
			)
			messages = append(messages, event.Message)
			for _, result := range event.ToolResults {
				messages = append(messages, result)
			}
			if err := r.session.registerMainMessages(
				snapshot.state,
				messages,
			); err != nil {
				return err
			}
		}
		if event.Type == agent.EventTypeInteractionEnd {
			if err := r.persistTurn(eventCtx, event.Messages, snapshot.state); err != nil {
				return err
			}
			persistedMessages += len(event.Messages)
		}
		if r.sink == nil {
			return nil
		}
		display := translateAgentEvent(event)
		if display == nil {
			return nil
		}
		return r.sink(eventCtx, *display)
	})

	messages := result.Messages()
	var persistErr error
	if persistedMessages > len(messages) {
		persistErr = fmt.Errorf(
			"app: persisted message count %d exceeds result count %d",
			persistedMessages,
			len(messages),
		)
	} else if persistedMessages < len(messages) {
		persistErr = r.persistTurn(
			ctx,
			messages[persistedMessages:],
			snapshot.state,
		)
	}
	if runErr != nil {
		return errors.Join(
			fmt.Errorf("app: run agent: %w", runErr),
			persistErr,
		)
	}
	return persistErr
}

// mailboxInputSource converts one delivery mailbox into an agent input
// source, translating deliveries into validated user messages.
func mailboxInputSource(
	take func() (interaction.Delivery, bool),
	label string,
) agent.InputSource {
	return func() (agent.InputMessage, bool, error) {
		delivery, ok := take()
		if !ok {
			return agent.InputMessage{}, false, nil
		}
		message, err := llm.NewUserMessage(
			llm.NewTextContent(delivery.Text).Part(),
		)
		if err != nil {
			return agent.InputMessage{}, false, fmt.Errorf(
				"app: create %s message: %w",
				label,
				err,
			)
		}
		return agent.InputMessage{
			ID:      delivery.ID,
			Message: message,
		}, true, nil
	}
}

// beginMainRun freezes the main run's history and settings, then registers
// its initial user input for concurrent side-thread snapshots. Only one main
// run may own the interactive Session at a time.
func (s *interactiveSession) beginMainRun(
	prompt llm.UserMessage,
) (mainRunSnapshot, error) {
	settings := s.settingsSnapshot()
	if settings.loop == nil {
		return mainRunSnapshot{}, credentialNotConfiguredError(
			s.providers,
			settings.configuration,
		)
	}

	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.activeMainRun != nil {
		return mainRunSnapshot{}, fmt.Errorf("app: another main run is active")
	}
	history, err := cloneAgentMessages(s.history)
	if err != nil {
		return mainRunSnapshot{}, err
	}
	pendingMessages, err := cloneAgentMessages([]llm.AgentMessage{prompt})
	if err != nil {
		return mainRunSnapshot{}, err
	}
	state := &mainRunState{pendingMessages: pendingMessages}
	s.activeMainRun = state
	return mainRunSnapshot{
		state:        state,
		loop:         settings.loop,
		history:      history,
		model:        settings.model,
		options:      settings.options,
		systemPrompt: settings.systemPrompt,
	}, nil
}

// registerMainMessages makes accepted user input and complete model/tool turns
// visible to side snapshots while the current main interaction is still
// running. Streaming assistant output is never registered.
func (s *interactiveSession) registerMainMessages(
	state *mainRunState,
	messages []llm.AgentMessage,
) error {
	cloned, err := cloneAgentMessages(messages)
	if err != nil {
		return err
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.activeMainRun != state {
		return fmt.Errorf("app: main run state is no longer active")
	}
	state.pendingMessages = append(state.pendingMessages, cloned...)
	return nil
}

// endMainRun removes transient user inputs even when the run or Session
// persistence fails. The identity check prevents a stale run from clearing a
// newer owner.
func (s *interactiveSession) endMainRun(state *mainRunState) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.activeMainRun == state {
		state.pendingMessages = []llm.AgentMessage{}
		s.activeMainRun = nil
	}
}

// commitHistory serializes durable Session updates without holding the
// in-memory history lock across file I/O. After persistence succeeds, one
// short critical section publishes the complete interaction and clears its
// transient inputs, so a side snapshot observes one consistent version.
func (s *interactiveSession) commitHistory(
	ctx context.Context,
	state *mainRunState,
	messages []llm.AgentMessage,
) error {
	cloned, err := cloneAgentMessages(messages)
	if err != nil {
		return err
	}
	s.historySyncMu.Lock()
	defer s.historySyncMu.Unlock()
	if err := appendSessionTurn(ctx, s.store, messages); err != nil {
		return err
	}

	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.history = append(s.history, cloned...)
	if s.activeMainRun == state {
		state.pendingMessages = []llm.AgentMessage{}
	}
	return nil
}

func (r *interactiveRun) persistTurn(
	ctx context.Context,
	messages []llm.AgentMessage,
	state *mainRunState,
) error {
	if len(messages) == 0 {
		return nil
	}
	return r.session.commitHistory(ctx, state, messages)
}
