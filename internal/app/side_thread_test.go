package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
)

// sideHarness wires a real interactiveSession with an injectable model
// factory so side-thread tests exercise the production composition path.
type sideHarness struct {
	t            *testing.T
	application  *application
	session      *interactiveSession
	store        *session.Store
	storePath    string
	workspace    string
	factoryCalls atomic.Int32
}

// newSideHarness constructs an application and interactiveSession with a
// counting model factory. newModel is invoked once for the main loop during
// setup and once per NewSideThread afterwards.
func newSideHarness(
	t *testing.T,
	newModel func() (agent.Model, error),
) *sideHarness {
	t.Helper()

	harness := &sideHarness{
		t:         t,
		workspace: t.TempDir(),
	}
	harness.storePath = filepath.Join(t.TempDir(), "conversation.jsonl")
	harness.store = createAppTestSession(t, harness.storePath, harness.workspace)
	harness.application = &application{dependencies: dependencies{
		newModel: func(configuration config.Config) (agent.Model, error) {
			harness.factoryCalls.Add(1)
			return newModel()
		},
		saveSetting: func(config.Setting, string) error { return nil },
		providers:   defaultProviders(),
	}}
	configuration := config.Config{
		Provider:       string(deepseek.ProviderID),
		Model:          deepseek.DefaultModel().ID,
		Thinking:       llm.ThinkingLevelMedium,
		DeepSeekAPIKey: "test-key",
	}
	model := deepseek.DefaultModel()
	model.ThinkingLevels = slices.Clone(model.ThinkingLevels)
	model.InputModalities = slices.Clone(model.InputModalities)
	loop, err := harness.application.newAgentLoop(configuration, nil)
	if err != nil {
		t.Fatalf("newAgentLoop() error = %v", err)
	}
	harness.session = &interactiveSession{
		application:   harness.application,
		loop:          loop,
		store:         harness.store,
		model:         model,
		options:       llm.StreamOptions{Thinking: llm.ClampThinkingLevel(model, configuration.Thinking)},
		configuration: configuration,
		systemPrompt:  "side harness system prompt",
		providers:     defaultProviders(),
	}
	t.Cleanup(func() {
		if err := harness.store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	return harness
}

// runSide executes one side question on a side runner and returns the
// completed run.
func runSide(
	t *testing.T,
	runner interaction.Runner,
	prompt string,
) error {
	t.Helper()
	active, err := runner.NewRun(interaction.RunInput{Prompt: prompt}, nil)
	if err != nil {
		return err
	}
	return active.Run(t.Context())
}

// requestMessageTexts flattens the visible text of one recorded request in
// message order.
func requestMessageTexts(request llm.Request) []string {
	texts := make([]string, 0)
	for _, message := range request.Messages {
		switch value := message.(type) {
		case llm.UserMessage:
			for _, part := range value.Content {
				if part.Type == llm.ContentTypeText {
					texts = append(texts, part.Text)
				}
			}
		case llm.AssistantMessage:
			for _, part := range value.Content {
				if part.Type == llm.ContentTypeText {
					texts = append(texts, part.Text)
				}
			}
		}
	}
	return texts
}

// TestSideThreadEmptySnapshotStaysFrozenAfterParentCommits is the tracer
// bullet: a side thread created while the parent history is empty must never
// observe history the parent commits afterwards.
func TestSideThreadEmptySnapshotStaysFrozenAfterParentCommits(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}

	// The parent commits one full interaction after the side thread exists.
	if err := runInteractive(t.Context(), harness.session, "main question", nil); err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	if len(mainModel.requests) != 1 {
		t.Fatalf("main requests = %d, want 1", len(mainModel.requests))
	}

	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	if len(sideModel.requests) != 1 {
		t.Fatalf("side requests = %d, want 1", len(sideModel.requests))
	}
	request := sideModel.requests[0]
	if len(request.Tools) != 0 {
		t.Fatalf("side request tools = %v, want none", request.Tools)
	}
	if got, want := requestMessageTexts(request), []string{"side question"}; !slices.Equal(got, want) {
		t.Fatalf("side request messages = %v, want %v", got, want)
	}
}

// TestSideThreadFreezesNonemptySnapshotAndSettings proves that a side thread
// created over a nonempty parent history freezes that history and the
// selected model, stream options, configuration, and system prompt at
// creation: later parent mutations must never leak into side requests.
func TestSideThreadFreezesNonemptySnapshotAndSettings(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	model := llm.Model{
		ID:               "test-model",
		Name:             "Test Model",
		API:              "test-api",
		Provider:         "test-provider",
		SupportsThinking: true,
		ThinkingLevels: []llm.ThinkingLevel{
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
		},
		InputModalities: []llm.InputModality{
			llm.InputModalityText,
			llm.InputModalityImage,
		},
		ContextWindow: 65_536,
		MaxTokens:     8_192,
	}
	harness.session.model = model

	// Commit one parent interaction before the side thread exists.
	if err := runInteractive(t.Context(), harness.session, "main question", nil); err != nil {
		t.Fatalf("main Run() error = %v", err)
	}

	temperature := 0.7
	harness.session.options.Temperature = &temperature

	factoryCallsAfterMain := harness.factoryCalls.Load()
	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	// Exactly one additional model service was constructed, for the side
	// thread; the main service was not recreated.
	if got, want := harness.factoryCalls.Load(), factoryCallsAfterMain+1; got != want {
		t.Fatalf("model factory calls after NewSideThread = %d, want %d", got, want)
	}

	// Mutate every parent-owned value the side thread must have frozen.
	harness.session.systemPrompt = "changed system prompt"
	harness.session.configuration.Model = "changed-model"
	model.ThinkingLevels[0] = llm.ThinkingLevelXHigh
	model.InputModalities[0] = llm.InputModalityUnknown
	temperature = 0.9
	harness.session.history[0].(llm.UserMessage).Content[0].Text = "MUTATED MAIN QUESTION"
	if err := runInteractive(t.Context(), harness.session, "second main question", nil); err != nil {
		t.Fatalf("second main Run() error = %v", err)
	}

	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	if len(sideModel.requests) != 1 {
		t.Fatalf("side requests = %d, want 1", len(sideModel.requests))
	}
	request := sideModel.requests[0]

	if got, want := requestMessageTexts(request), []string{
		"main question",
		"main answer",
		"side question",
	}; !slices.Equal(got, want) {
		t.Fatalf("side request messages = %v, want %v", got, want)
	}
	if got, want := request.SystemPrompt, sideThreadSystemPrompt(
		"side harness system prompt",
	); got != want {
		t.Fatalf("side request system prompt = %q, want %q", got, want)
	}
	if got, want := request.Model.ThinkingLevels, []llm.ThinkingLevel{
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
	}; !slices.Equal(got, want) {
		t.Fatalf("side request thinking levels = %v, want %v", got, want)
	}
	if got, want := request.Model.InputModalities, []llm.InputModality{
		llm.InputModalityText,
		llm.InputModalityImage,
	}; !slices.Equal(got, want) {
		t.Fatalf("side request input modalities = %v, want %v", got, want)
	}
	if request.Options.Temperature == nil {
		t.Fatal("side request temperature is nil, want frozen 0.7")
	}
	if got, want := *request.Options.Temperature, 0.7; got != want {
		t.Fatalf("side request temperature = %v, want %v", got, want)
	}
	if len(request.Tools) != 0 {
		t.Fatalf("side request tools = %v, want none", request.Tools)
	}
}

// TestSideThreadFactoryErrorPropagates proves NewSideThread returns the model
// construction error and no runner when the side service cannot be built.
func TestSideThreadFactoryErrorPropagates(t *testing.T) {
	t.Parallel()

	calls := 0
	boom := errors.New("model construction failed")
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return &recordingModel{response: "main answer"}, nil
		}
		return nil, boom
	})

	side, err := harness.session.NewSideThread()
	if !errors.Is(err, boom) {
		t.Fatalf("NewSideThread() error = %v, want %v", err, boom)
	}
	if side != nil {
		t.Fatalf("NewSideThread() runner = %v, want nil", side)
	}
}

func TestSideRunRejectsMainRunDeliveries(t *testing.T) {
	t.Parallel()

	calls := 0
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		return &recordingModel{response: "answer"}, nil
	})
	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	active, err := side.NewRun(
		interaction.RunInput{Prompt: "side question"},
		nil,
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	err = active.Deliver(interaction.Delivery{
		ID:   "steer-1",
		Text: "do not steer",
		Kind: interaction.DeliveryKindSteer,
	})
	if !errors.Is(err, interaction.ErrClosed) {
		t.Fatalf("Deliver() error = %v, want ErrClosed", err)
	}
}

// TestSideThreadReusesServiceAcrossSequentialRuns proves the side model
// service and loop are constructed exactly once at creation and reused by
// every sequential run on the same side runner.
func TestSideThreadReusesServiceAcrossSequentialRuns(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	factoryCalls := harness.factoryCalls.Load()
	for index := 0; index < 2; index++ {
		if err := runSide(t, side, "side question"); err != nil {
			t.Fatalf("side run %d error = %v", index+1, err)
		}
	}
	if got, want := harness.factoryCalls.Load(), factoryCalls; got != want {
		t.Fatalf("model factory calls after side runs = %d, want %d", got, want)
	}
	if got, want := len(sideModel.requests), 2; got != want {
		t.Fatalf("side service requests = %d, want %d", got, want)
	}
}

// TestSideThreadSequentialRunsFormPrivateMultiTurn proves sequential runs on
// the same side runner accumulate private in-memory context: each new run
// sees the frozen parent snapshot plus every prior completed side
// interaction.
func TestSideThreadSequentialRunsFormPrivateMultiTurn(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	if err := runInteractive(t.Context(), harness.session, "main question", nil); err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	if err := runSide(t, side, "side question 1"); err != nil {
		t.Fatalf("first side Run() error = %v", err)
	}
	if err := runSide(t, side, "side question 2"); err != nil {
		t.Fatalf("second side Run() error = %v", err)
	}

	if got, want := len(sideModel.requests), 2; got != want {
		t.Fatalf("side service requests = %d, want 2", got)
	}
	if got, want := requestMessageTexts(sideModel.requests[0]), []string{
		"main question",
		"main answer",
		"side question 1",
	}; !slices.Equal(got, want) {
		t.Fatalf("first side request messages = %v, want %v", got, want)
	}
	if got, want := requestMessageTexts(sideModel.requests[1]), []string{
		"main question",
		"main answer",
		"side question 1",
		"side answer",
		"side question 2",
	}; !slices.Equal(got, want) {
		t.Fatalf("second side request messages = %v, want %v", got, want)
	}
}

func TestSideThreadBoundsPrivateHistory(t *testing.T) {
	t.Parallel()

	calls := 0
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return &recordingModel{response: "main answer"}, nil
		}
		return sideModel, nil
	})
	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	for index := 1; index <= maximumSideInteractions+2; index++ {
		prompt := "side question " + strconv.Itoa(index)
		if err := runSide(t, side, prompt); err != nil {
			t.Fatalf("side run %d error = %v", index, err)
		}
	}

	texts := requestMessageTexts(sideModel.requests[len(sideModel.requests)-1])
	if got, want := len(texts), maximumSideInteractions*2+1; got != want {
		t.Fatalf("last side request text count = %d, want %d", got, want)
	}
	if slices.Contains(texts, "side question 1") {
		t.Fatalf("last side request retained expired question: %v", texts)
	}
	if !slices.Contains(texts, "side question 2") {
		t.Fatalf("last side request omitted oldest retained question: %v", texts)
	}
}

// waitFor polls condition until it holds or the deadline passes.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}

// gatedModel records every request and blocks selected 1-based request
// indexes mid-stream until released or the context is cancelled. A gated
// request emits preDelta (when set) as one text delta before blocking, so
// tests can hold a run with partial assistant output in flight.
type gatedModel struct {
	mu       sync.Mutex
	requests []llm.Request
	gates    map[int]chan struct{}
	preDelta string
	response string
}

func (m *gatedModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	index := len(m.requests)
	release, gated := m.gates[index]
	preDelta := m.preDelta
	response := m.response
	m.mu.Unlock()

	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{llm.NewTextContent(response).Part()}
	message.StopReason = llm.StopReasonStop
	post := []llm.Event{
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{Type: llm.EventTypeDone, StopReason: llm.StopReasonStop, Message: &message},
	}
	prefix := []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
	}
	if !gated {
		events := append(prefix, llm.Event{
			Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: response,
		})
		events = append(events, post...)
		return &eventStream{events: events}, nil
	}
	return &gatedStream{
		ctx:      ctx,
		release:  release,
		prefix:   prefix,
		post:     post,
		preDelta: preDelta,
	}, nil
}

func (m *gatedModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// request returns the 1-based recorded request.
func (m *gatedModel) request(index int) llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[index-1]
}

// gatedStream emits the start prefix, then one optional partial delta, then
// blocks on the release channel until released or the context is cancelled.
type gatedStream struct {
	ctx       context.Context
	release   chan struct{}
	prefix    []llm.Event
	post      []llm.Event
	preDelta  string
	index     int
	deltaSent bool
	released  bool
}

func (s *gatedStream) Next() (llm.Event, error) {
	if s.index < len(s.prefix) {
		event := s.prefix[s.index]
		s.index++
		return event, nil
	}
	if s.preDelta != "" && !s.deltaSent {
		s.deltaSent = true
		return llm.Event{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: s.preDelta}, nil
	}
	if !s.released {
		select {
		case <-s.release:
			s.released = true
		case <-s.ctx.Done():
			return llm.Event{}, s.ctx.Err()
		}
	}
	if len(s.post) == 0 {
		return llm.Event{}, io.EOF
	}
	event := s.post[0]
	s.post = s.post[1:]
	return event, nil
}

func (s *gatedStream) Close() error { return nil }

// TestSideThreadSnapshotIncludesActivePromptBeforeFirstCommit proves that a
// side snapshot taken while a main run is active before its first
// interaction commits contains that run's initial prompt exactly once, no
// partial assistant deltas, and no early persistence to history or the
// durable store.
func TestSideThreadSnapshotIncludesActivePromptBeforeFirstCommit(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &gatedModel{
		response: "main answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
		preDelta: "main partial",
	}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mainDone := make(chan error, 1)
	go func() {
		active, err := harness.session.NewRun(
			interaction.RunInput{Prompt: "main question"},
			nil,
		)
		if err != nil {
			mainDone <- err
			return
		}
		mainDone <- active.Run(ctx)
	}()
	waitFor(t, func() bool { return mainModel.requestCount() >= 1 })

	// The main run is now blocked mid-stream after emitting one partial
	// delta. Nothing may be persisted or visible to a side snapshot yet.
	storeBefore, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(storeBefore, []byte("main question")) {
		t.Fatal("main prompt persisted to the store before its first commit")
	}
	harness.session.historyMu.Lock()
	historyLen := len(harness.session.history)
	harness.session.historyMu.Unlock()
	if historyLen != 0 {
		t.Fatalf("session history length = %d, want 0 before first commit", historyLen)
	}

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	if got, want := requestMessageTexts(sideModel.requests[0]), []string{
		"main question",
		"side question",
	}; !slices.Equal(got, want) {
		t.Fatalf("side request messages = %v, want %v", got, want)
	}

	// Releasing the gate lets the main interaction commit durably.
	close(mainModel.gates[1])
	if err := <-mainDone; err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	storeAfter, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Contains(storeAfter, []byte("main question")) {
		t.Fatal("committed main prompt missing from the store")
	}
}

// TestSideThreadSnapshotIncludesCurrentFollowUp proves that a side snapshot
// taken while a main follow-up is running contains the committed initial
// interaction exactly once plus the accepted follow-up, but no in-progress
// assistant output.
func TestSideThreadSnapshotIncludesCurrentFollowUp(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &gatedModel{
		response: "main answer",
		gates: map[int]chan struct{}{
			1: make(chan struct{}),
			2: make(chan struct{}),
		},
	}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active, err := harness.session.NewRun(
		interaction.RunInput{Prompt: "main question"},
		nil,
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	mainDone := make(chan error, 1)
	go func() { mainDone <- active.Run(ctx) }()
	waitFor(t, func() bool { return mainModel.requestCount() >= 1 })

	// Queue the follow-up before releasing the first request so the main run
	// polls it at its natural stop, then release interaction 1.
	if err := active.Deliver(interaction.Delivery{
		ID:   "follow-1",
		Text: "follow up text",
		Kind: interaction.DeliveryKindFollowUp,
	}); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	close(mainModel.gates[1])
	waitFor(t, func() bool { return mainModel.requestCount() >= 2 })

	// The main run is now in its follow-up window: the first interaction has
	// durably committed and the follow-up request is blocked.
	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	if got, want := requestMessageTexts(sideModel.requests[0]), []string{
		"main question",
		"main answer",
		"follow up text",
		"side question",
	}; !slices.Equal(got, want) {
		t.Fatalf("side request messages = %v, want %v", got, want)
	}

	close(mainModel.gates[2])
	if err := <-mainDone; err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	data, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Contains(data, []byte("follow up text")) {
		t.Fatal("committed follow-up missing from the store")
	}
}

// TestSideThreadTurnsLeaveParentStateUntouched proves side exchanges never
// leak into the main Session: the durable store stays byte-identical, main
// history and usage are unchanged, and a later main request contains no side
// messages.
func TestSideThreadTurnsLeaveParentStateUntouched(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{
		response: "side answer",
		usage: llm.Usage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
			Cost:         &llm.Cost{Total: 1.5},
		},
	}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	if err := runInteractive(t.Context(), harness.session, "main question", nil); err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	storeBefore, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	harness.session.historyMu.Lock()
	historyBefore := slices.Clone(harness.session.history)
	harness.session.historyMu.Unlock()
	usageBefore := harness.session.totalUsage

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	for index, prompt := range []string{"side question 1", "side question 2"} {
		if err := runSide(t, side, prompt); err != nil {
			t.Fatalf("side run %d error = %v", index+1, err)
		}
	}

	storeAfter, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(storeBefore, storeAfter) {
		t.Fatal("side turns changed the durable Session store")
	}
	harness.session.historyMu.Lock()
	historyAfter := slices.Clone(harness.session.history)
	harness.session.historyMu.Unlock()
	if !slices.EqualFunc(historyBefore, historyAfter, func(a, b llm.AgentMessage) bool {
		return reflect.DeepEqual(a, b)
	}) {
		t.Fatal("side turns changed the main history")
	}
	if !reflect.DeepEqual(harness.session.totalUsage, usageBefore) {
		t.Fatalf("main usage changed from %v to %v", usageBefore, harness.session.totalUsage)
	}

	// A later main request must contain no side messages.
	if err := runInteractive(t.Context(), harness.session, "main follow question", nil); err != nil {
		t.Fatalf("follow-up main Run() error = %v", err)
	}
	last := mainModel.requests[len(mainModel.requests)-1]
	for _, text := range requestMessageTexts(last) {
		if strings.Contains(text, "side ") {
			t.Fatalf("later main request contains side text %q", text)
		}
	}
}

// TestSideThreadCancelDoesNotAffectMainRun proves cancelling an in-flight
// side request reaches only that side run: the main run stays blocked and
// completes normally afterwards.
func TestSideThreadCancelDoesNotAffectMainRun(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &gatedModel{
		response: "main answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
	}
	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
	}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active, err := harness.session.NewRun(
		interaction.RunInput{Prompt: "main question"},
		nil,
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	mainDone := make(chan error, 1)
	go func() { mainDone <- active.Run(ctx) }()
	waitFor(t, func() bool { return mainModel.requestCount() >= 1 })

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	sideCtx, sideCancel := context.WithCancel(t.Context())
	sideActive, err := side.NewRun(
		interaction.RunInput{Prompt: "side question"},
		nil,
	)
	if err != nil {
		t.Fatalf("side NewRun() error = %v", err)
	}
	sideDone := make(chan error, 1)
	go func() { sideDone <- sideActive.Run(sideCtx) }()
	waitFor(t, func() bool { return sideModel.requestCount() >= 1 })

	sideCancel()
	if err := <-sideDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("side Run() error = %v, want context.Canceled", err)
	}

	// The main run was never cancelled and completes normally.
	close(mainModel.gates[1])
	if err := <-mainDone; err != nil {
		t.Fatalf("main Run() error = %v", err)
	}
	data, err := os.ReadFile(harness.storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Contains(data, []byte("main question")) {
		t.Fatal("committed main prompt missing from the store")
	}
}

// TestMainRunCancelDoesNotAffectSideThread proves cancelling an in-flight
// main request reaches only the main run: a side thread created afterwards
// still runs to completion.
func TestMainRunCancelDoesNotAffectSideThread(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &gatedModel{
		response: "main answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
		preDelta: "main partial",
	}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	mainCtx, mainCancel := context.WithCancel(t.Context())
	active, err := harness.session.NewRun(
		interaction.RunInput{Prompt: "main question"},
		nil,
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	mainDone := make(chan error, 1)
	go func() { mainDone <- active.Run(mainCtx) }()
	waitFor(t, func() bool { return mainModel.requestCount() >= 1 })

	mainCancel()
	if err := <-mainDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("main Run() error = %v, want context.Canceled", err)
	}

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	if got, want := requestMessageTexts(sideModel.requests[0]), []string{
		"main question",
		"agent run canceled before completion",
		"side question",
	}; !slices.Equal(got, want) {
		t.Fatalf("side request messages = %v, want %v", got, want)
	}
}

// TestSideRunnerUsableAfterCancelledSideRun proves a cancelled side run
// invents no partial interaction in private history: completed prior side
// interactions remain, and the same side runner serves the next run
// normally.
func TestSideRunnerUsableAfterCancelledSideRun(t *testing.T) {
	t.Parallel()

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{2: make(chan struct{})},
	}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}
	if err := runSide(t, side, "side question 1"); err != nil {
		t.Fatalf("first side Run() error = %v", err)
	}

	sideCtx, sideCancel := context.WithCancel(t.Context())
	sideActive, err := side.NewRun(
		interaction.RunInput{Prompt: "cancelled side question"},
		nil,
	)
	if err != nil {
		t.Fatalf("side NewRun() error = %v", err)
	}
	sideDone := make(chan error, 1)
	go func() { sideDone <- sideActive.Run(sideCtx) }()
	waitFor(t, func() bool { return sideModel.requestCount() >= 2 })
	sideCancel()
	if err := <-sideDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("side Run() error = %v, want context.Canceled", err)
	}

	if err := runSide(t, side, "side question 3"); err != nil {
		t.Fatalf("third side Run() error = %v", err)
	}
	// The completed first interaction remains; the cancelled prompt and its
	// aborted terminal must not appear.
	if got, want := requestMessageTexts(sideModel.request(3)), []string{
		"side question 1",
		"side answer",
		"side question 3",
	}; !slices.Equal(got, want) {
		t.Fatalf("third side request messages = %v, want %v", got, want)
	}
}

// TestSideThreadDeepCloneIsolationRichNestedFields proves the frozen
// snapshot deep-copies every mutable field of every transcript message
// variant: image bytes, tool-call raw arguments, tool-result content,
// assistant usage cost, and compaction summaries. Mutating the parent after
// creation must not leak into side requests, and mutating a recorded side
// request must not leak back into the parent.
func TestSideThreadDeepCloneIsolationRichNestedFields(t *testing.T) {
	t.Parallel()

	image := &llm.ImageContent{Data: []byte("image-bytes"), MIMEType: "image/png"}
	call := &llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"/tmp/x"}`),
	}
	cost := &llm.Cost{Input: 1, Output: 2, Total: 3}
	history := []llm.AgentMessage{
		llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{
				{Type: llm.ContentTypeImage, Image: image},
			},
			Timestamp: 1,
		},
		llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				llm.NewTextContent("assistant text").Part(),
				{Type: llm.ContentTypeToolCall, ToolCall: call},
			},
			API:        "test-api",
			Provider:   "test-provider",
			ModelID:    "test-model",
			StopReason: llm.StopReasonToolUse,
			Timestamp:  2,
		},
		llm.ToolResultMessage{
			Role:       llm.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    []llm.ContentPart{llm.NewTextContent("tool output").Part()},
			Timestamp:  3,
		},
		llm.AssistantMessage{
			Role:       llm.RoleAssistant,
			Content:    []llm.ContentPart{llm.NewTextContent("final answer").Part()},
			API:        "test-api",
			Provider:   "test-provider",
			ModelID:    "test-model",
			Usage:      llm.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3, Cost: cost},
			StopReason: llm.StopReasonStop,
			Timestamp:  4,
		},
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "compacted context",
			TokensBefore: 100,
			Timestamp:    5,
		},
	}

	calls := 0
	mainModel := &recordingModel{response: "main answer"}
	sideModel := &recordingModel{response: "side answer"}
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		return sideModel, nil
	})
	harness.session.historyMu.Lock()
	harness.session.history = history
	harness.session.historyMu.Unlock()

	side, err := harness.session.NewSideThread()
	if err != nil {
		t.Fatalf("NewSideThread() error = %v", err)
	}

	// Mutate every parent-owned nested field after creation. The tool-call
	// arguments stay valid JSON so the side request can still be projected.
	image.Data[0] = 'X'
	call.Arguments = json.RawMessage(`{"path":"/tmp/y"}`)
	cost.Total = 999
	history[1].(llm.AssistantMessage).Content[0].Text = "MUTATED ASSISTANT TEXT"
	history[2].(llm.ToolResultMessage).Content[0].Text = "MUTATED TOOL OUTPUT"

	if err := runSide(t, side, "side question"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	request := sideModel.requests[0]
	if got, want := len(request.Messages), 6; got != want {
		t.Fatalf("side request message count = %d, want %d", got, want)
	}
	user, ok := request.Messages[0].(llm.UserMessage)
	if !ok {
		t.Fatalf("request message 0 = %T, want UserMessage", request.Messages[0])
	}
	if user.Content[0].Image == nil {
		t.Fatal("request image content is nil")
	}
	if got, want := string(user.Content[0].Image.Data), "image-bytes"; got != want {
		t.Fatalf("request image data = %q, want %q", got, want)
	}
	assistant, ok := request.Messages[1].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("request message 1 = %T, want AssistantMessage", request.Messages[1])
	}
	if assistant.Content[1].ToolCall == nil {
		t.Fatal("request tool call is nil")
	}
	if got, want := string(assistant.Content[1].ToolCall.Arguments), `{"path":"/tmp/x"}`; got != want {
		t.Fatalf("request tool call arguments = %q, want %q", got, want)
	}
	if got, want := assistant.Content[0].Text, "assistant text"; got != want {
		t.Fatalf("request assistant text = %q, want %q", got, want)
	}
	toolResult, ok := request.Messages[2].(llm.ToolResultMessage)
	if !ok {
		t.Fatalf("request message 2 = %T, want ToolResultMessage", request.Messages[2])
	}
	if got, want := toolResult.Content[0].Text, "tool output"; got != want {
		t.Fatalf("request tool result content = %q, want %q", got, want)
	}
	costAssistant, ok := request.Messages[3].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("request message 3 = %T, want AssistantMessage", request.Messages[3])
	}
	if costAssistant.Usage.Cost == nil {
		t.Fatal("request usage cost is nil")
	}
	if got, want := costAssistant.Usage.Cost.Total, 3.0; got != want {
		t.Fatalf("request usage cost total = %v, want %v", got, want)
	}
	summary, ok := request.Messages[4].(llm.UserMessage)
	if !ok {
		t.Fatalf("request message 4 = %T, want projected UserMessage", request.Messages[4])
	}
	if !strings.Contains(summary.Content[0].Text, "compacted context") {
		t.Fatalf("request compaction summary = %q, want it to contain the summary", summary.Content[0].Text)
	}

	// Reverse direction: mutating the recorded request must not leak back
	// into the parent history.
	user.Content[0].Image.Data[0] = 'Y'
	assistant.Content[1].ToolCall.Arguments[0] = 'Y'
	costAssistant.Usage.Cost.Total = 777
	if got, want := string(image.Data), "Xmage-bytes"; got != want {
		t.Fatalf("parent image data after request mutation = %q, want %q", got, want)
	}
	if got, want := string(call.Arguments), `{"path":"/tmp/y"}`; got != want {
		t.Fatalf("parent tool call arguments after request mutation = %q, want %q", got, want)
	}
	if got, want := cost.Total, 999.0; got != want {
		t.Fatalf("parent usage cost after request mutation = %v, want %v", got, want)
	}
}

// TestSideThreadConcurrentSnapshotsConsistent drives a gated main run
// through multiple committed interactions and follow-ups while side threads
// are created concurrently, settings commands churn the selected model, and
// the session history is reloaded from the store. Every observed side
// snapshot must be a complete interaction prefix with the initial main
// prompt exactly once: never a half turn, a duplicate, or a torn settings
// pair.
func TestSideThreadConcurrentSnapshotsConsistent(t *testing.T) {
	t.Parallel()

	mainModel := &gatedModel{
		response: "main answer",
		gates: map[int]chan struct{}{
			1: make(chan struct{}),
			2: make(chan struct{}),
			3: make(chan struct{}),
		},
	}
	var sideModelsMu sync.Mutex
	sideModels := []*recordingModel{}
	calls := 0
	harness := newSideHarness(t, func() (agent.Model, error) {
		calls++
		if calls == 1 {
			return mainModel, nil
		}
		model := &recordingModel{response: "side answer"}
		sideModelsMu.Lock()
		sideModels = append(sideModels, model)
		sideModelsMu.Unlock()
		return model, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active, err := harness.session.NewRun(
		interaction.RunInput{Prompt: "main question"},
		nil,
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	mainDone := make(chan error, 1)
	go func() { mainDone <- active.Run(ctx) }()

	// Settings churn: alternate /model between the two catalog models while
	// side threads are created.
	settingsDone := make(chan struct{})
	var settingsWG sync.WaitGroup
	settingsWG.Add(2)
	go func() {
		defer settingsWG.Done()
		ids := []string{"deepseek-v4-flash", "deepseek-v4-pro"}
		for index := 0; ; index++ {
			select {
			case <-settingsDone:
				return
			default:
			}
			if _, err := harness.session.RunSlashCommand(
				t.Context(),
				interaction.CommandRequest{
					Name:      "model",
					Arguments: ids[index%len(ids)],
				},
			); err != nil {
				t.Errorf("RunSlashCommand(/model) error = %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer settingsWG.Done()
		for {
			select {
			case <-settingsDone:
				return
			default:
			}
			_ = harness.session.SlashCommands()
			_ = harness.session.RuntimeState()
			_ = harness.session.settingsInformation()
			time.Sleep(time.Millisecond)
		}
	}()

	// History reload churn: rebuild the in-memory history from the store
	// while commits and side snapshots happen.
	reloadDone := make(chan struct{})
	var reloadWG sync.WaitGroup
	reloadWG.Add(1)
	go func() {
		defer reloadWG.Done()
		for {
			select {
			case <-reloadDone:
				return
			default:
			}
			if err := harness.session.reloadHistory(); err != nil {
				t.Errorf("reloadHistory() error = %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	expected := [][]string{
		{"main question", "side question 1"},
		{"main question", "main answer", "follow up 1", "side question 2"},
		{
			"main question",
			"main answer",
			"follow up 1",
			"main answer",
			"follow up 2",
			"side question 3",
		},
	}
	for index := 1; index <= 3; index++ {
		waitFor(t, func() bool { return mainModel.requestCount() >= index })

		side, err := harness.session.NewSideThread()
		if err != nil {
			t.Fatalf("NewSideThread() error = %v", err)
		}
		prompt := "side question " + strconv.Itoa(index)
		if err := runSide(t, side, prompt); err != nil {
			t.Fatalf("side Run() error = %v", err)
		}
		sideModelsMu.Lock()
		sideModel := sideModels[len(sideModels)-1]
		sideModelsMu.Unlock()
		request := sideModel.requests[0]
		texts := requestMessageTexts(request)
		if got, want := texts, expected[index-1]; !slices.Equal(got, want) {
			t.Fatalf("side request %d messages = %v, want %v", index, got, want)
		}
		if count := strings.Count(strings.Join(texts, "\n"), "main question"); count != 1 {
			t.Fatalf("side request %d contains the initial main prompt %d times, want exactly once", index, count)
		}
		if request.Model.ID != "deepseek-v4-flash" && request.Model.ID != "deepseek-v4-pro" {
			t.Fatalf("side request %d model id = %q, want a catalog model", index, request.Model.ID)
		}

		if index < 3 {
			if err := active.Deliver(interaction.Delivery{
				ID:   "follow-" + strconv.Itoa(index),
				Text: "follow up " + strconv.Itoa(index),
				Kind: interaction.DeliveryKindFollowUp,
			}); err != nil {
				t.Fatalf("Deliver() error = %v", err)
			}
		}
		close(mainModel.gates[index])
	}
	if err := <-mainDone; err != nil {
		t.Fatalf("main Run() error = %v", err)
	}

	close(settingsDone)
	settingsWG.Wait()
	close(reloadDone)
	reloadWG.Wait()
}
