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

var _ interaction.ActiveRun = (*interactiveRun)(nil)

func (s *interactiveSession) NewRun(
	input interaction.RunInput,
	sink interaction.EventSink,
) (interaction.ActiveRun, error) {
	if s.loop == nil {
		return nil, credentialNotConfiguredError(s.providers, s.configuration)
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

	persistedMessages := 0
	result, runErr := r.session.loop.Run(ctx, agent.RunInput{
		Model:        r.session.model,
		SystemPrompt: r.session.systemPrompt,
		History:      r.session.history,
		Prompt:       r.prompt,
		Options:      r.session.options,
		Steering:     r.inputSource(r.mailbox.TakeSteering, "steering"),
		FollowUp:     r.inputSource(r.mailbox.TakeFollowUp, "follow-up"),
	}, func(eventCtx context.Context, event agent.AgentEvent) error {
		if event.Type == agent.EventTypeInteractionEnd {
			if err := r.persistTurn(eventCtx, event.Messages); err != nil {
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
		persistErr = r.persistTurn(ctx, messages[persistedMessages:])
	}
	if runErr != nil {
		return errors.Join(
			fmt.Errorf("app: run agent: %w", runErr),
			persistErr,
		)
	}
	return persistErr
}

func (r *interactiveRun) inputSource(
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

func (r *interactiveRun) persistTurn(
	ctx context.Context,
	messages []llm.AgentMessage,
) error {
	if len(messages) == 0 {
		return nil
	}
	if err := appendSessionTurn(ctx, r.session.store, messages); err != nil {
		return err
	}
	r.session.history = append(r.session.history, messages...)
	return nil
}
