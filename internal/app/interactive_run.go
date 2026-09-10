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
	model     llm.Model
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
	ctx context.Context,
	input interaction.RunInput,
	sink interaction.EventSink,
) (interaction.ActiveRun, error) {
	if s == nil {
		return nil, fmt.Errorf("app: interactive Session is required")
	}
	settings := s.settingsSnapshot()
	if settings.modelErr != nil {
		return nil, settings.modelErr
	}
	if settings.loop == nil {
		return nil, credentialNotConfiguredError(
			s.providers,
			settings.configuration,
		)
	}
	input, err := prepareFileInput(ctx, input, s.workspace, s.guardAdapter, s.handleGuardAsk)
	if err != nil {
		return nil, err
	}
	prompt, err := newImageInputContext(ctx, input, settings.model)
	if err != nil {
		return nil, fmt.Errorf("app: create prompt: %w", err)
	}
	if err := s.ensureSessionStore(ctx); err != nil {
		return nil, err
	}
	return &interactiveRun{
		session: s,
		prompt:  prompt,
		sink:    sink,
		mailbox: interaction.NewMailbox(),
		model:   settings.model,
	}, nil
}

// ensureSessionStore lazily creates the session file when the first prompt
// is accepted, using the same cancellable preparation context.
func (s *interactiveSession) ensureSessionStore(ctx context.Context) error {
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()
	if s.conversation.store != nil {
		return nil
	}
	if s.workspace == nil {
		return fmt.Errorf("app: workspace is required")
	}
	store, err := createDefaultSession(ctx, s.workspace)
	if err != nil {
		return err
	}
	s.conversation.store = store
	return nil
}
func (r *interactiveRun) Deliver(ctx context.Context, delivery interaction.Delivery) error {
	if r == nil || r.mailbox == nil {
		return interaction.ErrClosed
	}
	r.mu.Lock()
	model := r.model
	r.mu.Unlock()
	if err := validateImageModel(model, delivery.Images); err != nil {
		return err
	}
	if r.session == nil {
		return interaction.ErrClosed
	}
	input, err := prepareFileInput(ctx, interaction.RunInput{Prompt: delivery.Text, Images: delivery.Images, Files: delivery.Files}, r.session.workspace, r.session.guardAdapter, r.session.handleGuardAsk)
	if err != nil {
		return err
	}
	message, err := newImageInputContext(ctx, input, model)
	if err != nil {
		return err
	}
	delivery.Text = input.Prompt
	delivery.Images = nil
	delivery.Files = nil
	for _, part := range message.Content {
		if part.Image != nil {
			delivery.Images = append(delivery.Images, part.Image.Clone())
		}
	}
	if err := ctx.Err(); err != nil {
		return err
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
	r.mu.Lock()
	r.model = snapshot.model
	r.mu.Unlock()
	ctx, err = modelSessionContext(ctx, r.session.conversation.store)
	if err != nil {
		return err
	}

	configured := configuredModel{
		configuration: snapshot.configuration,
		model:         snapshot.model,
		options:       snapshot.options,
	}
	// This run-local projection includes completed messages even while a tool
	// group is not yet replay-safe for the conversation's side snapshots.
	contextHistory := append([]llm.AgentMessage(nil), snapshot.history...)
	_, runErr := snapshot.loop.Run(ctx, agent.RunInput{
		Model:        snapshot.model,
		SystemPrompt: snapshot.systemPrompt,
		History:      snapshot.history,
		Prompt:       r.prompt,
		Options:      snapshot.options,
		MessageRecorder: func(recordCtx context.Context, message llm.AgentMessage) error {
			if err := r.session.conversation.recordMessage(recordCtx, snapshot.state, message); err != nil {
				return err
			}
			contextHistory = append(contextHistory, message)
			return nil
		},
		Compactor: func(compactCtx context.Context, history []llm.AgentMessage) ([]llm.AgentMessage, error) {
			compacted, err := r.session.compactHistory(compactCtx, history, &configured)
			if err == nil {
				contextHistory = append([]llm.AgentMessage(nil), compacted...)
			}
			return compacted, err
		},
		Steering: mailboxInputSource(r.mailbox.TakeSteering, "steering", snapshot.model),
		FollowUp: mailboxInputSource(r.mailbox.TakeFollowUp, "follow-up", snapshot.model),
	}, func(eventCtx context.Context, event agent.AgentEvent) error {
		if r.sink == nil {
			return nil
		}
		display := translateAgentEvent(event)
		if display == nil {
			return nil
		}
		if display.Kind != interaction.EventAssistantDelta {
			usage := contextDisplay(snapshot.model, snapshot.configuration,
				snapshot.systemPrompt, r.session.tools, contextHistory)
			display.Context = &usage
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
	model llm.Model,
) agent.InputSource {
	return func() (agent.InputMessage, bool, error) {
		delivery, ok := take()
		if !ok {
			return agent.InputMessage{}, false, nil
		}
		message, err := newImageInput(interaction.RunInput{
			Prompt: delivery.Text, Images: delivery.Images,
		}, model)
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
	if settings.modelErr != nil {
		return mainRunSnapshot{}, settings.modelErr
	}
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

	history, err := s.application.compactHistory(ctx, s.conversation.store, currentHistory, configured, nil)
	if err != nil {
		return nil, err
	}
	s.conversation.historyMu.Lock()
	s.conversation.history = history
	s.conversation.historyMu.Unlock()
	return cloneAgentMessages(history)
}
