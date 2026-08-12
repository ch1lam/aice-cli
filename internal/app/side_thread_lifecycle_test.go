package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// fakeClock is a deterministic, mutex-protected time source for lifecycle
// tests. The registry reads it through interactiveSession.sideClock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// newSideLifecycleHarness wires a side harness to a fake clock. The model
// factory must be safe for concurrent use when the test creates threads from
// multiple goroutines.
func newSideLifecycleHarness(
	t *testing.T,
	newModel func() (agent.Model, error),
) (*sideHarness, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	harness := newSideHarness(t, newModel)
	harness.session.sideClock = clock.Now
	return harness, clock
}

// startSideRunAsync prepares and starts one side run on a fresh goroutine,
// returning a channel that receives its terminal error.
func startSideRunAsync(
	t *testing.T,
	runner interaction.Runner,
	prompt string,
	ctx context.Context,
) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	active, err := runner.NewRun(interaction.RunInput{Prompt: prompt}, nil)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	go func() { done <- active.Run(ctx) }()
	return done
}

// TestSideThreadWritableWindowBoundary pins the 20-minute follow-up window
// with >= as the cutoff: writable before, read-only at and after exactly
// 20 minutes of idle, and a run that starts before the cutoff but whose
// answer terminates inside the window restarts the clock.
func TestSideThreadWritableWindowBoundary(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	thread, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}
	if got := thread.Status; got != interaction.SideThreadWritable {
		t.Fatalf("new thread status = %v, want writable", got)
	}

	clock.Advance(sideWritableIdle - time.Second)
	if got := harness.session.SideThreads()[0].Status; got != interaction.SideThreadWritable {
		t.Fatalf("status at 19m59s idle = %v, want writable", got)
	}
	// A run still starts inside the writable window, and its termination
	// restarts the idle clock.
	if err := runSide(t, runner, "follow up"); err != nil {
		t.Fatalf("run inside writable window error = %v", err)
	}

	// Exactly 20 minutes after that termination the thread turns read-only.
	clock.Advance(sideWritableIdle)
	if got := harness.session.SideThreads()[0].Status; got != interaction.SideThreadReadOnly {
		t.Fatalf("status at exactly 20m idle = %v, want read-only", got)
	}
	if err := runSide(t, runner, "too late"); !errors.Is(
		err,
		interaction.ErrSideThreadReadOnly,
	) {
		t.Fatalf("run at exactly 20m idle error = %v, want ErrSideThreadReadOnly", err)
	}

	clock.Advance(time.Second)
	if got := harness.session.SideThreads()[0].Status; got != interaction.SideThreadReadOnly {
		t.Fatalf("status at 20m1s idle = %v, want read-only", got)
	}
}

// TestSideThreadExpiryBoundary pins the 120-minute lifetime with >= as the
// cutoff: the thread survives at 119m59s and is permanently deleted at
// exactly 120 minutes.
func TestSideThreadExpiryBoundary(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	thread, _, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	clock.Advance(sideExpiryIdle - time.Second)
	if got := len(harness.session.SideThreads()); got != 1 {
		t.Fatalf("threads at 119m59s idle = %d, want 1", got)
	}

	clock.Advance(time.Second)
	if got := len(harness.session.SideThreads()); got != 0 {
		t.Fatalf("threads at exactly 120m idle = %d, want 0", got)
	}
	if _, _, err := harness.session.OpenSideThread(thread.ID); !errors.Is(
		err,
		interaction.ErrSideThreadNotFound,
	) {
		t.Fatalf("OpenSideThread() after expiry error = %v, want ErrSideThreadNotFound", err)
	}
}

// TestSideThreadRunningSurvivesExpiryThreshold proves a running answer is
// never cancelled by crossing the 20/120-minute thresholds: the thread stays
// listed as running, close is refused, and the completed answer restarts the
// idle clock so the thread is writable again.
func TestSideThreadRunningSurvivesExpiryThreshold(t *testing.T) {
	t.Parallel()

	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
	}
	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return sideModel, nil
	})
	thread, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	done := startSideRunAsync(t, runner, "question", t.Context())
	waitFor(t, func() bool { return sideModel.requestCount() >= 1 })

	clock.Advance(sideExpiryIdle + 10*time.Minute)

	threads := harness.session.SideThreads()
	if got := len(threads); got != 1 {
		t.Fatalf("running thread at 130m idle = %d, want 1", got)
	}
	if got := threads[0].Status; got != interaction.SideThreadRunning {
		t.Fatalf("running thread status = %v, want running", got)
	}
	if err := harness.session.CloseSideThread(thread.ID); !errors.Is(
		err,
		interaction.ErrSideThreadRunning,
	) {
		t.Fatalf("CloseSideThread() while running error = %v, want ErrSideThreadRunning", err)
	}

	close(sideModel.gates[1])
	if err := <-done; err != nil {
		t.Fatalf("side Run() error = %v", err)
	}

	threads = harness.session.SideThreads()
	if got := len(threads); got != 1 {
		t.Fatalf("threads after running past expiry = %d, want 1", got)
	}
	if got := threads[0].Status; got != interaction.SideThreadWritable {
		t.Fatalf("thread status after long answer = %v, want writable", got)
	}
}

// TestSideThreadLastActiveAtUpdatedOnSuccess proves a successful answer
// termination moves the idle clock forward.
func TestSideThreadLastActiveAtUpdatedOnSuccess(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	_, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	clock.Advance(5 * time.Minute)
	if err := runSide(t, runner, "follow up"); err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
	want := clock.Now()
	if got := harness.session.SideThreads()[0].LastActiveAt; !got.Equal(want) {
		t.Fatalf("last active after success = %v, want %v", got, want)
	}
}

// TestSideThreadLastActiveAtUpdatedOnCancellation proves a cancelled answer
// also terminates the run and restarts the idle clock.
func TestSideThreadLastActiveAtUpdatedOnCancellation(t *testing.T) {
	t.Parallel()

	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
	}
	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return sideModel, nil
	})
	_, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := startSideRunAsync(t, runner, "question", ctx)
	waitFor(t, func() bool { return sideModel.requestCount() >= 1 })

	clock.Advance(7 * time.Minute)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("side Run() error = %v, want context.Canceled", err)
	}
	want := clock.Now()
	if got := harness.session.SideThreads()[0].LastActiveAt; !got.Equal(want) {
		t.Fatalf("last active after cancellation = %v, want %v", got, want)
	}
}

// TestSideThreadLastActiveAtUpdatedOnProviderError proves a failed answer
// terminates the run, restarts the idle clock, and leaves the thread usable.
func TestSideThreadLastActiveAtUpdatedOnProviderError(t *testing.T) {
	t.Parallel()

	boom := errors.New("provider exploded")
	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &failThenSucceedModel{fail: boom, response: "side answer"}, nil
	})
	_, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	clock.Advance(3 * time.Minute)
	if err := runSide(t, runner, "question"); err == nil {
		t.Fatal("side Run() error = nil, want provider error")
	}
	want := clock.Now()
	if got := harness.session.SideThreads()[0].LastActiveAt; !got.Equal(want) {
		t.Fatalf("last active after provider error = %v, want %v", got, want)
	}
	if err := runSide(t, runner, "again"); err != nil {
		t.Fatalf("follow-up after provider error error = %v", err)
	}
}

// TestSideThreadLimitReached proves the fifth thread is accepted, the sixth
// is refused with ErrSideThreadLimit, and closing one frees the slot.
func TestSideThreadLimitReached(t *testing.T) {
	t.Parallel()

	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	created := make([]interaction.SideThread, 0, maximumSideThreads)
	for index := 0; index < maximumSideThreads; index++ {
		thread, _, err := harness.session.CreateSideThread("question")
		if err != nil {
			t.Fatalf("CreateSideThread() %d error = %v", index+1, err)
		}
		created = append(created, thread)
	}
	if _, _, err := harness.session.CreateSideThread("overflow"); !errors.Is(
		err,
		interaction.ErrSideThreadLimit,
	) {
		t.Fatalf("sixth CreateSideThread() error = %v, want ErrSideThreadLimit", err)
	}

	if err := harness.session.CloseSideThread(created[0].ID); err != nil {
		t.Fatalf("CloseSideThread() error = %v", err)
	}
	if _, _, err := harness.session.CreateSideThread("after close"); err != nil {
		t.Fatalf("CreateSideThread() after close error = %v", err)
	}
}

// TestSideThreadConcurrencyLimit proves at most two threads answer at once:
// the third run is refused at start while two others are in flight and
// succeeds once a slot frees.
func TestSideThreadConcurrencyLimit(t *testing.T) {
	t.Parallel()

	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{1: make(chan struct{}), 2: make(chan struct{})},
	}
	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return sideModel, nil
	})
	_, first, err := harness.session.CreateSideThread("first")
	if err != nil {
		t.Fatalf("CreateSideThread() first error = %v", err)
	}
	_, second, err := harness.session.CreateSideThread("second")
	if err != nil {
		t.Fatalf("CreateSideThread() second error = %v", err)
	}
	_, third, err := harness.session.CreateSideThread("third")
	if err != nil {
		t.Fatalf("CreateSideThread() third error = %v", err)
	}

	firstDone := startSideRunAsync(t, first, "first question", t.Context())
	waitFor(t, func() bool { return sideModel.requestCount() >= 1 })
	secondDone := startSideRunAsync(t, second, "second question", t.Context())
	waitFor(t, func() bool { return sideModel.requestCount() >= 2 })

	if err := runSide(t, third, "third question"); !errors.Is(
		err,
		interaction.ErrSideThreadConcurrencyLimit,
	) {
		t.Fatalf("third concurrent run error = %v, want ErrSideThreadConcurrencyLimit", err)
	}
	if got := len(harness.session.SideThreads()); got != 3 {
		t.Fatalf("live threads = %d, want 3", got)
	}

	close(sideModel.gates[1])
	close(sideModel.gates[2])
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if err := runSide(t, third, "third question"); err != nil {
		t.Fatalf("third run after slots freed error = %v", err)
	}
}

// TestSideThreadBusyRejectsOverlapOnSameThread proves one thread never runs
// two answers at once.
func TestSideThreadBusyRejectsOverlapOnSameThread(t *testing.T) {
	t.Parallel()

	sideModel := &gatedModel{
		response: "side answer",
		gates:    map[int]chan struct{}{1: make(chan struct{})},
	}
	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return sideModel, nil
	})
	_, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	done := startSideRunAsync(t, runner, "question", t.Context())
	waitFor(t, func() bool { return sideModel.requestCount() >= 1 })

	if err := runSide(t, runner, "overlap"); !errors.Is(err, interaction.ErrSideThreadBusy) {
		t.Fatalf("overlapping run error = %v, want ErrSideThreadBusy", err)
	}

	close(sideModel.gates[1])
	if err := <-done; err != nil {
		t.Fatalf("side Run() error = %v", err)
	}
}

// TestSideThreadCloseDeletesAndIDsNeverReuse proves an explicit close is a
// real deletion: the thread disappears from every entry point and the next
// thread gets a fresh, strictly higher id.
func TestSideThreadCloseDeletesAndIDsNeverReuse(t *testing.T) {
	t.Parallel()

	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	first, _, err := harness.session.CreateSideThread("first")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	if err := harness.session.CloseSideThread(first.ID); err != nil {
		t.Fatalf("CloseSideThread() error = %v", err)
	}
	if got := len(harness.session.SideThreads()); got != 0 {
		t.Fatalf("threads after close = %d, want 0", got)
	}
	if _, _, err := harness.session.OpenSideThread(first.ID); !errors.Is(
		err,
		interaction.ErrSideThreadNotFound,
	) {
		t.Fatalf("OpenSideThread() after close error = %v, want ErrSideThreadNotFound", err)
	}

	second, _, err := harness.session.CreateSideThread("second")
	if err != nil {
		t.Fatalf("CreateSideThread() after close error = %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("new thread id = %d after close of %d, want a fresh higher id", second.ID, first.ID)
	}
}

// TestSideThreadExpiredThreadsFreeLimitSlots proves expiry purges threads
// before the count limit is evaluated, so expired threads never block new
// ones.
func TestSideThreadExpiredThreadsFreeLimitSlots(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	for index := 0; index < maximumSideThreads; index++ {
		if _, _, err := harness.session.CreateSideThread("question"); err != nil {
			t.Fatalf("CreateSideThread() %d error = %v", index+1, err)
		}
	}
	if _, _, err := harness.session.CreateSideThread("overflow"); !errors.Is(
		err,
		interaction.ErrSideThreadLimit,
	) {
		t.Fatalf("CreateSideThread() error = %v, want ErrSideThreadLimit", err)
	}

	clock.Advance(sideExpiryIdle)
	if _, _, err := harness.session.CreateSideThread("after expiry"); err != nil {
		t.Fatalf("CreateSideThread() after expiry error = %v", err)
	}
}

// TestSideThreadConcurrentCreationUniqueIDs races many creators against the
// registry and proves exactly the limit succeeds, every id is unique, and
// every rejection is the limit sentinel.
func TestSideThreadConcurrentCreationUniqueIDs(t *testing.T) {
	t.Parallel()

	var factoryCalls atomic.Int32
	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		factoryCalls.Add(1)
		return &recordingModel{response: "side answer"}, nil
	})

	const creators = 16
	ids := make(chan uint64, creators)
	limitErrors := make(chan error, creators)
	var wg sync.WaitGroup
	for index := 0; index < creators; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			thread, _, err := harness.session.CreateSideThread("concurrent question")
			if err != nil {
				limitErrors <- err
				return
			}
			ids <- thread.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(limitErrors)

	seen := make(map[uint64]struct{}, maximumSideThreads)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate side thread id %d", id)
		}
		seen[id] = struct{}{}
	}
	if got, want := len(seen), maximumSideThreads; got != want {
		t.Fatalf("created threads = %d, want %d", got, want)
	}
	limitCount := 0
	for err := range limitErrors {
		if !errors.Is(err, interaction.ErrSideThreadLimit) {
			t.Fatalf("creation error = %v, want ErrSideThreadLimit", err)
		}
		limitCount++
	}
	if got, want := limitCount, creators-maximumSideThreads; got != want {
		t.Fatalf("limit rejections = %d, want %d", got, want)
	}
}

// TestSideThreadListSortedByLastActivityAndDefensive proves the listing is
// newest activity first and that mutating the returned slice cannot reach
// the registry.
func TestSideThreadListSortedByLastActivityAndDefensive(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	first, _, err := harness.session.CreateSideThread("first")
	if err != nil {
		t.Fatalf("CreateSideThread() first error = %v", err)
	}
	clock.Advance(10 * time.Minute)
	second, _, err := harness.session.CreateSideThread("second")
	if err != nil {
		t.Fatalf("CreateSideThread() second error = %v", err)
	}
	clock.Advance(10 * time.Minute)
	third, _, err := harness.session.CreateSideThread("third")
	if err != nil {
		t.Fatalf("CreateSideThread() third error = %v", err)
	}

	threads := harness.session.SideThreads()
	if got, want := []uint64{
		threads[0].ID,
		threads[1].ID,
		threads[2].ID,
	}, []uint64{third.ID, second.ID, first.ID}; !slices.Equal(got, want) {
		t.Fatalf("list order = %v, want %v", got, want)
	}
	if got := threads[0].Status; got != interaction.SideThreadWritable {
		t.Fatalf("newest thread status = %v, want writable", got)
	}
	if got := threads[1].Status; got != interaction.SideThreadWritable {
		t.Fatalf("second thread status = %v, want writable", got)
	}
	if got := threads[2].Status; got != interaction.SideThreadReadOnly {
		t.Fatalf("oldest thread status = %v, want read-only at exactly 20m idle", got)
	}

	threads[0].Title = "MUTATED"
	if got := harness.session.SideThreads()[0].Title; got == "MUTATED" {
		t.Fatal("mutation of the returned list leaked into the registry")
	}
}

// TestSideThreadRunStartRevalidatesAfterOpen proves the run-start gate is
// the authority: a runner held open across the read-only boundary fails at
// start even though OpenSideThread succeeded earlier.
func TestSideThreadRunStartRevalidatesAfterOpen(t *testing.T) {
	t.Parallel()

	harness, clock := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	_, runner, err := harness.session.CreateSideThread("question")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}

	clock.Advance(sideWritableIdle)
	if _, _, err := harness.session.OpenSideThread(
		harness.session.SideThreads()[0].ID,
	); err != nil {
		t.Fatalf("OpenSideThread() error = %v", err)
	}
	if err := runSide(t, runner, "too late"); !errors.Is(
		err,
		interaction.ErrSideThreadReadOnly,
	) {
		t.Fatalf("run after open error = %v, want ErrSideThreadReadOnly", err)
	}
}

// failThenSucceedModel fails the first stream request and serves a plain
// answer afterwards, so tests can prove a thread survives a provider error.
type failThenSucceedModel struct {
	mu       sync.Mutex
	calls    int
	fail     error
	response string
}

func (m *failThenSucceedModel) Stream(
	ctx context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.mu.Lock()
	m.calls++
	fail := m.calls == 1
	m.mu.Unlock()
	if fail {
		return nil, m.fail
	}
	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{llm.NewTextContent(m.response).Part()}
	message.StopReason = llm.StopReasonStop
	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: m.response},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{
			Type:       llm.EventTypeDone,
			StopReason: llm.StopReasonStop,
			Message:    &message,
		},
	}}, nil
}

// TestSideThreadTitleDerivedFromFirstQuestion proves titles come from the
// first question: the first line, trimmed, and truncated with an ellipsis.
func TestSideThreadTitleDerivedFromFirstQuestion(t *testing.T) {
	t.Parallel()

	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	thread, _, err := harness.session.CreateSideThread("first line\nsecond line")
	if err != nil {
		t.Fatalf("CreateSideThread() error = %v", err)
	}
	if got, want := thread.Title, "first line"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}

	long := strings.Repeat("x", maxSideThreadTitleRunes+10)
	thread, _, err = harness.session.CreateSideThread(long)
	if err != nil {
		t.Fatalf("CreateSideThread() long error = %v", err)
	}
	if got, want := len([]rune(thread.Title)), maxSideThreadTitleRunes+1; got != want {
		t.Fatalf("truncated title runes = %d, want %d", got, want)
	}
	if !strings.HasSuffix(thread.Title, "…") {
		t.Fatalf("truncated title = %q, want ellipsis suffix", thread.Title)
	}
}

// TestSideThreadEmptyQuestionRejected proves the registry refuses a thread
// without a first question.
func TestSideThreadEmptyQuestionRejected(t *testing.T) {
	t.Parallel()

	harness, _ := newSideLifecycleHarness(t, func() (agent.Model, error) {
		return &recordingModel{response: "side answer"}, nil
	})
	if _, _, err := harness.session.CreateSideThread("   "); err == nil {
		t.Fatal("CreateSideThread() with an empty question succeeded")
	}
}
