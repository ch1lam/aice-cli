package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
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
	state         *mainRunState
	loop          *agent.Loop
	history       []llm.AgentMessage
	model         llm.Model
	options       llm.StreamOptions
	systemPrompt  string
	configuration config.Config
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
// recordMessage with the run's own context.
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

	configured := configuredModel{
		configuration: snapshot.configuration,
		model:         snapshot.model,
		options:       snapshot.options,
	}
	_, runErr := snapshot.loop.Run(ctx, agent.RunInput{
		Model:        snapshot.model,
		SystemPrompt: snapshot.systemPrompt,
		History:      snapshot.history,
		Prompt:       r.prompt,
		Options:      snapshot.options,
		MessageRecorder: func(recordCtx context.Context, message llm.AgentMessage) error {
			return r.session.conversation.recordMessage(recordCtx, snapshot.state, message)
		},
		Compactor: func(compactCtx context.Context, history []llm.AgentMessage) ([]llm.AgentMessage, error) {
			return r.session.compactHistory(compactCtx, history, &configured)
		},
		Steering: mailboxInputSource(r.mailbox.TakeSteering, "steering"),
		FollowUp: mailboxInputSource(r.mailbox.TakeFollowUp, "follow-up"),
	}, func(eventCtx context.Context, event agent.AgentEvent) error {
		if r.sink == nil {
			return nil
		}
		display := translateAgentEvent(event)
		if display == nil {
			return nil
		}
		return r.sink(eventCtx, *display)
	})

	if runErr != nil {
		return fmt.Errorf("app: run agent: %w", runErr)
	}
	return nil
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
		state:         state,
		loop:          settings.loop,
		history:       history,
		model:         settings.model,
		options:       settings.options,
		systemPrompt:  settings.systemPrompt,
		configuration: settings.configuration,
	}, nil
}

func (s *interactiveSession) compactHistory(
	ctx context.Context,
	currentHistory []llm.AgentMessage,
	configured *configuredModel,
) ([]llm.AgentMessage, error) {
	if s == nil || s.application == nil {
		return nil, fmt.Errorf("app: interactive Session is not initialized")
	}
	if s.conversation.store == nil {
		return nil, fmt.Errorf("app: interactive Session store is required")
	}

	// Serialize checkpoints with source-message commits and explicit checkout.
	// The Loop supplies complete context at a paired model-round boundary.
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()

	history, err := s.application.compactHistory(ctx, s.conversation.store, currentHistory, configured)
	if err != nil {
		return nil, err
	}
	s.conversation.historyMu.Lock()
	s.conversation.history = history
	s.conversation.historyMu.Unlock()
	return cloneAgentMessages(history)
}
