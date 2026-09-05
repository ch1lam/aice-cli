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
	if err := s.ensureSessionStore(); err != nil {
		return nil, err
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

// ensureSessionStore lazily creates the session file when the first prompt
// is accepted. File creation is local disk I/O without a caller context,
// so it uses a background context; every later turn appends through
// commitHistory with the run's own context.
func (s *interactiveSession) ensureSessionStore() error {
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()
	if s.conversation.store != nil {
		return nil
	}
	if s.workspace == nil {
		return fmt.Errorf("app: workspace is required")
	}
	store, err := createDefaultSession(context.Background(), s.workspace)
	if err != nil {
		return err
	}
	s.conversation.store = store
	return nil
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
	defer r.session.conversation.endMainRun(snapshot.state)

	persistedMessages := 0
	result, runErr := snapshot.loop.Run(ctx, agent.RunInput{
		Model:        snapshot.model,
		SystemPrompt: snapshot.systemPrompt,
		History:      snapshot.history,
		Prompt:       r.prompt,
		Options:      snapshot.options,
		Compactor:    r.session.compactHistory,
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
			if err := r.session.conversation.registerMainMessages(
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
			if err := r.session.conversation.registerMainMessages(
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

	state, history, err := s.conversation.beginMainRun(prompt)
	if err != nil {
		return mainRunSnapshot{}, err
	}
	return mainRunSnapshot{
		state:        state,
		loop:         settings.loop,
		history:      history,
		model:        settings.model,
		options:      settings.options,
		systemPrompt: settings.systemPrompt,
	}, nil
}

func (s *interactiveSession) compactHistory(
	ctx context.Context,
	_ []llm.AgentMessage,
) ([]llm.AgentMessage, error) {
	if s == nil || s.application == nil {
		return nil, fmt.Errorf("app: interactive Session is not initialized")
	}
	if s.conversation.store == nil {
		return nil, fmt.Errorf("app: interactive Session store is required")
	}

	// Serialize automatic checkpoints with turn commits and explicit checkout.
	// The model run is already at a complete interaction boundary here, so the
	// durable store and the callback's history describe the same context.
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()

	history, err := s.application.compactHistory(ctx, s.conversation.store)
	if err != nil {
		return nil, err
	}
	s.conversation.historyMu.Lock()
	s.conversation.history = history
	s.conversation.historyMu.Unlock()
	return cloneAgentMessages(history)
}

func (r *interactiveRun) persistTurn(
	ctx context.Context,
	messages []llm.AgentMessage,
	state *mainRunState,
) error {
	if len(messages) == 0 {
		return nil
	}
	return r.session.conversation.commitHistory(ctx, state, messages)
}
