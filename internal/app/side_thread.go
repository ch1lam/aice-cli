package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maximumSideInteractions = 20
	sideThreadInstruction   = "You are answering an ephemeral side question. " +
		"Use only the supplied conversation context, do not use tools, and " +
		"answer concisely. Do not claim to inspect information that is absent " +
		"from the context."
)

var _ interaction.SideThreadFactory = (*interactiveSession)(nil)

// sideRunner is one isolated, tool-free side thread. It owns a frozen
// snapshot of the parent Session history captured at NewSideThread and a
// private in-memory history of its own completed interactions. It never
// writes to the parent Session store, history, transcript, or usage.
type sideRunner struct {
	loop         *agent.Loop
	model        llm.Model
	options      llm.StreamOptions
	systemPrompt string
	snapshot     []llm.AgentMessage

	mu        sync.Mutex
	history   [][]llm.AgentMessage
	isRunning bool
}

// NewSideThread freezes the parent context and constructs a distinct
// tool-free model service and agent loop for the side thread. A failure to
// construct the service or loop returns an error and no runner.
func (s *interactiveSession) NewSideThread() (interaction.Runner, error) {
	if s == nil || s.application == nil {
		return nil, fmt.Errorf("app: application is required")
	}
	snapshot, err := s.sideSnapshot()
	if err != nil {
		return nil, fmt.Errorf("app: snapshot parent context: %w", err)
	}
	settings := s.settingsSnapshot()
	loop, err := s.application.newAgentLoop(settings.configuration, nil)
	if err != nil {
		return nil, err
	}
	return &sideRunner{
		loop:         loop,
		model:        settings.model,
		options:      settings.options,
		systemPrompt: sideThreadSystemPrompt(settings.systemPrompt),
		snapshot:     snapshot,
		history:      make([][]llm.AgentMessage, 0, maximumSideInteractions),
	}, nil
}

func sideThreadSystemPrompt(parent string) string {
	if strings.TrimSpace(parent) == "" {
		return sideThreadInstruction
	}
	return parent + "\n\n" + sideThreadInstruction
}

// sideSnapshot returns a deep clone of the committed parent history plus
// accepted user inputs and complete model/tool turns from the current main
// interaction. In-progress assistant output remains private to the main run.
func (s *interactiveSession) sideSnapshot() ([]llm.AgentMessage, error) {
	s.historyMu.RLock()
	defer s.historyMu.RUnlock()
	snapshot, err := cloneAgentMessages(s.history)
	if err != nil {
		return nil, err
	}
	if s.activeMainRun == nil {
		return snapshot, nil
	}
	pending, err := cloneAgentMessages(s.activeMainRun.pendingMessages)
	if err != nil {
		return nil, err
	}
	return append(snapshot, pending...), nil
}

// cloneModel deep-copies every mutable field of a model selection so a side
// thread's request metadata cannot alias the parent's live model state.
func cloneModel(model llm.Model) llm.Model {
	cloned := model
	cloned.ThinkingLevels = slices.Clone(model.ThinkingLevels)
	cloned.InputModalities = slices.Clone(model.InputModalities)
	return cloned
}

// cloneStreamOptions deep-copies every mutable field of stream options so a
// later parent temperature change cannot drift an existing side thread.
func cloneStreamOptions(options llm.StreamOptions) llm.StreamOptions {
	cloned := options
	if options.Temperature != nil {
		temperature := *options.Temperature
		cloned.Temperature = &temperature
	}
	return cloned
}

// cloneAgentMessages deep-copies a transcript slice so neither side can
// mutate the other's message values. Unknown message types fail closed.
func cloneAgentMessages(messages []llm.AgentMessage) ([]llm.AgentMessage, error) {
	if messages == nil {
		return nil, nil
	}
	cloned := make([]llm.AgentMessage, len(messages))
	for index, message := range messages {
		copied, err := cloneAgentMessage(message)
		if err != nil {
			return nil, fmt.Errorf("app: clone agent message %d: %w", index, err)
		}
		cloned[index] = copied
	}
	return cloned, nil
}

// cloneAgentMessage deep-copies one transcript message. All known variants
// are copied structurally; an unknown variant fails closed instead of
// aliasing.
func cloneAgentMessage(message llm.AgentMessage) (llm.AgentMessage, error) {
	switch value := message.(type) {
	case llm.UserMessage:
		return llm.UserMessage{
			Role:      value.Role,
			Content:   cloneContentParts(value.Content),
			Timestamp: value.Timestamp,
		}, nil
	case llm.AssistantMessage:
		copied := value
		copied.Content = cloneContentParts(value.Content)
		if value.Usage.Cost != nil {
			cost := *value.Usage.Cost
			copied.Usage.Cost = &cost
		}
		return copied, nil
	case llm.ToolResultMessage:
		copied := value
		copied.Content = cloneContentParts(value.Content)
		return copied, nil
	case llm.CompactionSummaryMessage:
		return value, nil
	case nil:
		return nil, fmt.Errorf("app: clone agent message: message is nil")
	default:
		return nil, fmt.Errorf("app: clone agent message: unsupported type %T", message)
	}
}

// cloneContentParts deep-copies one message's content slice, including image
// bytes, tool-call raw arguments, and recursively nested tool-result content,
// so no mutable field can alias between a frozen snapshot and the parent.
func cloneContentParts(parts []llm.ContentPart) []llm.ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]llm.ContentPart, len(parts))
	for index, part := range parts {
		cloned[index] = part
		switch part.Type {
		case llm.ContentTypeImage:
			if part.Image != nil {
				image := *part.Image
				image.Data = slices.Clone(part.Image.Data)
				cloned[index].Image = &image
			}
		case llm.ContentTypeToolCall:
			if part.ToolCall != nil {
				call := *part.ToolCall
				call.Arguments = slices.Clone(part.ToolCall.Arguments)
				cloned[index].ToolCall = &call
			}
		case llm.ContentTypeToolResult:
			if part.ToolResult != nil {
				result := *part.ToolResult
				result.Content = cloneContentParts(part.ToolResult.Content)
				cloned[index].ToolResult = &result
			}
		}
	}
	return cloned
}

var _ interaction.Runner = (*sideRunner)(nil)

func (r *sideRunner) NewRun(
	input interaction.RunInput,
	sink interaction.EventSink,
) (interaction.ActiveRun, error) {
	prompt, err := llm.NewUserMessage(llm.NewTextContent(input.Prompt).Part())
	if err != nil {
		return nil, fmt.Errorf("app: create side prompt: %w", err)
	}
	return &sideRun{
		runner: r,
		prompt: prompt,
		sink:   sink,
	}, nil
}

// requestHistory returns a deep clone of the frozen parent snapshot followed
// by the side thread's bounded private interactions, so neither the runner nor
// the parent can be mutated through a recorded request.
func (r *sideRunner) requestHistory() ([]llm.AgentMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, err := cloneAgentMessages(r.snapshot)
	if err != nil {
		return nil, fmt.Errorf("app: clone side snapshot: %w", err)
	}
	count := len(snapshot)
	for _, interaction := range r.history {
		count += len(interaction)
	}
	history := make([]llm.AgentMessage, 0, count)
	history = append(history, snapshot...)
	for index, interaction := range r.history {
		cloned, err := cloneAgentMessages(interaction)
		if err != nil {
			return nil, fmt.Errorf("app: clone side interaction %d: %w", index, err)
		}
		history = append(history, cloned...)
	}
	return history, nil
}

// commitTurn appends one completed interaction to the side thread's bounded
// in-memory history. Older interactions are discarded after the newest 20.
func (r *sideRunner) commitTurn(messages []llm.AgentMessage) error {
	if len(messages) == 0 {
		return nil
	}
	cloned, err := cloneAgentMessages(messages)
	if err != nil {
		return fmt.Errorf("app: clone side turn: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = append(r.history, cloned)
	if len(r.history) > maximumSideInteractions {
		r.history = slices.Clone(r.history[len(r.history)-maximumSideInteractions:])
	}
	return nil
}

func (r *sideRunner) beginRun() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isRunning {
		return fmt.Errorf("app: side run already active")
	}
	r.isRunning = true
	return nil
}

func (r *sideRunner) endRun() {
	r.mu.Lock()
	r.isRunning = false
	r.mu.Unlock()
}

type sideRun struct {
	runner *sideRunner
	prompt llm.UserMessage
	sink   interaction.EventSink

	mu        sync.Mutex
	isStarted bool
}

var _ interaction.ActiveRun = (*sideRun)(nil)

func (r *sideRun) Deliver(interaction.Delivery) error {
	return interaction.ErrClosed
}

func (r *sideRun) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if r == nil || r.runner == nil || r.runner.loop == nil {
		return fmt.Errorf("app: side run is not initialized")
	}
	r.mu.Lock()
	if r.isStarted {
		r.mu.Unlock()
		return fmt.Errorf("app: side run already started")
	}
	r.isStarted = true
	r.mu.Unlock()
	if err := r.runner.beginRun(); err != nil {
		return err
	}
	defer r.runner.endRun()

	history, err := r.runner.requestHistory()
	if err != nil {
		return err
	}
	committed := 0
	result, runErr := r.runner.loop.Run(ctx, agent.RunInput{
		Model:        r.runner.model,
		SystemPrompt: r.runner.systemPrompt,
		History:      history,
		Prompt:       r.prompt,
		Options:      r.runner.options,
	}, func(eventCtx context.Context, event agent.AgentEvent) error {
		if event.Type == agent.EventTypeInteractionEnd {
			if err := r.runner.commitTurn(event.Messages); err != nil {
				return err
			}
			committed += len(event.Messages)
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
	if runErr != nil {
		return fmt.Errorf("app: run side agent: %w", runErr)
	}
	// A completed run always emits interaction_end for every interaction;
	// the tail covers messages that completed without one. A failed or
	// cancelled run must not invent a partial interaction in private
	// history, so nothing is committed on error.
	messages := result.Messages()
	if committed > len(messages) {
		return fmt.Errorf(
			"app: committed side message count %d exceeds result count %d",
			committed,
			len(messages),
		)
	}
	if committed < len(messages) {
		if err := r.runner.commitTurn(messages[committed:]); err != nil {
			return err
		}
	}
	return nil
}
