package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maximumSideInteractions   = 20
	maximumSideThreads        = 5
	maximumRunningSideThreads = 2
	// sideWritableIdle is the idle window after an answer terminates during
	// which the thread accepts follow-up questions. Idle is measured from
	// the most recent answer termination.
	sideWritableIdle = 20 * time.Minute
	// sideExpiryIdle is the lifetime after which a thread is permanently
	// deleted from the registry. Running answers cross the threshold
	// unharmed and restart the clock when they terminate.
	sideExpiryIdle = 120 * time.Minute
	// maxSideThreadTitleRunes bounds titles derived from the first question.
	maxSideThreadTitleRunes = 48
	sideThreadInstruction   = "You are answering an ephemeral side question. " +
		"Use only the supplied conversation context, do not use tools, and " +
		"answer concisely. Do not claim to inspect information that is absent " +
		"from the context."
)

var _ interaction.SideThreadManager = (*interactiveSession)(nil)

// sideThread is the registry entry for one ephemeral /btw thread. It owns
// exactly one runner whose lifecycle hooks report back into the registry, so
// the registry stays authoritative for running state, limits, and clocks.
type sideThread struct {
	id           uint64
	title        string
	runner       *sideRunner
	lastActiveAt time.Time
	isRunning    bool
}

// sideRunner is one isolated, tool-free side thread. It owns a frozen
// snapshot of the parent Session history captured at creation and a
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

	// begin and end are the registry-side run lifecycle hooks: begin gates
	// a run against read-only, expiry, and concurrency limits at run start;
	// end clears the running state and restarts the idle clock. Hooks are
	// nil only for runners constructed without a registry, which keeps the
	// runner itself usable in isolation.
	begin func() error
	end   func()
}

// SideThreads lists every live thread, most recently active first, deleting
// expired threads as part of the listing. The returned slice and its entries
// are defensive copies.
func (s *interactiveSession) SideThreads() []interaction.SideThread {
	if s == nil {
		return []interaction.SideThread{}
	}
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	now := s.sideNow()
	s.purgeExpiredThreadsLocked(now)
	threads := s.sideThreadsLocked()
	list := make([]interaction.SideThread, 0, len(threads))
	for _, thread := range threads {
		list = append(list, sideThreadView(thread, now))
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].LastActiveAt.Equal(list[j].LastActiveAt) {
			return list[i].ID > list[j].ID
		}
		return list[i].LastActiveAt.After(list[j].LastActiveAt)
	})
	return list
}

// CreateSideThread creates a new thread from its first question. It freezes
// the accepted parent context and settings at this moment, derives a title,
// and returns metadata plus a runner for the first interaction. The thread
// count limit is enforced here; run-start limits are enforced when the run
// actually begins.
func (s *interactiveSession) CreateSideThread(
	prompt string,
) (interaction.SideThread, interaction.Runner, error) {
	if s == nil || s.application == nil {
		return interaction.SideThread{}, nil, fmt.Errorf(
			"app: application is required",
		)
	}
	if strings.TrimSpace(prompt) == "" {
		return interaction.SideThread{}, nil, fmt.Errorf(
			"app: side question is required",
		)
	}
	snapshot, err := s.sideSnapshot()
	if err != nil {
		return interaction.SideThread{}, nil, fmt.Errorf(
			"app: snapshot parent context: %w",
			err,
		)
	}
	settings := s.settingsSnapshot()
	loop, err := s.application.newAgentLoop(settings.configuration, nil)
	if err != nil {
		return interaction.SideThread{}, nil, err
	}

	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	now := s.sideNow()
	s.purgeExpiredThreadsLocked(now)
	threads := s.sideThreadsLocked()
	if len(threads) >= maximumSideThreads {
		return interaction.SideThread{}, nil, interaction.ErrSideThreadLimit
	}
	s.sideNextID++
	id := s.sideNextID
	thread := &sideThread{
		id:           id,
		title:        sideThreadTitle(prompt),
		lastActiveAt: now,
	}
	thread.runner = &sideRunner{
		loop:         loop,
		model:        settings.model,
		options:      settings.options,
		systemPrompt: sideThreadSystemPrompt(settings.systemPrompt),
		snapshot:     snapshot,
		history:      make([][]llm.AgentMessage, 0, maximumSideInteractions),
		begin:        func() error { return s.beginSideRun(id) },
		end:          func() { s.endSideRun(id) },
	}
	threads[id] = thread
	return sideThreadView(thread, now), thread.runner, nil
}

// OpenSideThread looks up a live thread and returns its metadata plus the
// runner used for follow-up interactions. Expired threads have already been
// deleted and return ErrSideThreadNotFound. Whether a follow-up may actually
// start is revalidated when the runner begins, so holding a runner never
// bypasses read-only or concurrency limits.
func (s *interactiveSession) OpenSideThread(
	id uint64,
) (interaction.SideThread, interaction.Runner, error) {
	if s == nil {
		return interaction.SideThread{}, nil, interaction.ErrSideThreadNotFound
	}
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	now := s.sideNow()
	s.purgeExpiredThreadsLocked(now)
	thread, ok := s.sideThreads[id]
	if !ok {
		return interaction.SideThread{}, nil, interaction.ErrSideThreadNotFound
	}
	return sideThreadView(thread, now), thread.runner, nil
}

// CloseSideThread permanently deletes a thread. A thread with an answer in
// flight is refused with ErrSideThreadRunning so a caller never loses a
// running answer silently.
func (s *interactiveSession) CloseSideThread(id uint64) error {
	if s == nil {
		return interaction.ErrSideThreadNotFound
	}
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	now := s.sideNow()
	s.purgeExpiredThreadsLocked(now)
	thread, ok := s.sideThreads[id]
	if !ok {
		return interaction.ErrSideThreadNotFound
	}
	if thread.isRunning {
		return interaction.ErrSideThreadRunning
	}
	delete(s.sideThreads, id)
	return nil
}

// beginSideRun is the run-start gate for one side interaction. It runs on
// the runner's goroutine when a Run starts, so the registry remains the
// authority for read-only, expiry, and concurrency limits even if a
// frontend bypasses Create/Open validation.
func (s *interactiveSession) beginSideRun(id uint64) error {
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	now := s.sideNow()
	s.purgeExpiredThreadsLocked(now)
	thread, ok := s.sideThreads[id]
	if !ok {
		return interaction.ErrSideThreadNotFound
	}
	if thread.isRunning {
		return interaction.ErrSideThreadBusy
	}
	if s.sideRunning >= maximumRunningSideThreads {
		return interaction.ErrSideThreadConcurrencyLimit
	}
	if now.Sub(thread.lastActiveAt) >= sideWritableIdle {
		return interaction.ErrSideThreadReadOnly
	}
	thread.isRunning = true
	s.sideRunning++
	return nil
}

// endSideRun runs when a side interaction terminates for any reason: success,
// cancellation, or failure. It clears the running state, releases a
// concurrency slot, and restarts the idle clock.
func (s *interactiveSession) endSideRun(id uint64) {
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	thread, ok := s.sideThreads[id]
	if !ok {
		return
	}
	thread.isRunning = false
	if s.sideRunning > 0 {
		s.sideRunning--
	}
	thread.lastActiveAt = s.sideNow()
}

// purgeExpiredThreadsLocked permanently deletes every non-running thread
// whose idle time reached the expiry lifetime. Running answers cross the
// threshold unharmed and reset the clock when they terminate.
func (s *interactiveSession) purgeExpiredThreadsLocked(now time.Time) {
	for id, thread := range s.sideThreads {
		if thread.isRunning {
			continue
		}
		if now.Sub(thread.lastActiveAt) >= sideExpiryIdle {
			delete(s.sideThreads, id)
		}
	}
}

// sideThreadsLocked lazily initializes the thread registry. Callers hold
// sideMu.
func (s *interactiveSession) sideThreadsLocked() map[uint64]*sideThread {
	if s.sideThreads == nil {
		s.sideThreads = make(map[uint64]*sideThread)
	}
	return s.sideThreads
}

// sideNow returns the registry time, honoring an injected clock for tests.
func (s *interactiveSession) sideNow() time.Time {
	if s.sideClock != nil {
		return s.sideClock()
	}
	return time.Now()
}

// sideThreadView builds a defensive metadata snapshot. A negative idle
// (clock moved backwards) is treated as zero, which keeps the thread
// writable.
func sideThreadView(
	thread *sideThread,
	now time.Time,
) interaction.SideThread {
	status := interaction.SideThreadWritable
	switch {
	case thread.isRunning:
		status = interaction.SideThreadRunning
	case now.Sub(thread.lastActiveAt) >= sideWritableIdle:
		status = interaction.SideThreadReadOnly
	}
	return interaction.SideThread{
		ID:           thread.id,
		Title:        thread.title,
		Status:       status,
		LastActiveAt: thread.lastActiveAt,
	}
}

// sideThreadTitle derives the display title from the first question: the
// first line, trimmed and truncated to maxSideThreadTitleRunes runes.
func sideThreadTitle(prompt string) string {
	title := prompt
	if line, _, found := strings.Cut(prompt, "\n"); found {
		title = line
	}
	title = strings.TrimSpace(title)
	runes := []rune(title)
	if len(runes) > maxSideThreadTitleRunes {
		return string(runes[:maxSideThreadTitleRunes]) + "…"
	}
	return title
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
	// The registry hook runs first so its sentinel errors (busy, read-only,
	// concurrency limit) stay authoritative for runners owned by a registry.
	if r.begin != nil {
		if err := r.begin(); err != nil {
			return err
		}
	}
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
	if r.end != nil {
		r.end()
	}
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
