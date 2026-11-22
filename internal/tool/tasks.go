package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maxRunningTasks = 8
	// maxRunningAgents bounds concurrent subagents within maxRunningTasks:
	// each one spends model tokens.
	maxRunningAgents = 4
	maxRetainedTasks = 16
	// taskOutputLimit bounds one task's output file; a task that exceeds it
	// is terminated so a runaway log cannot fill the disk.
	taskOutputLimit = 64 << 20
	// taskMaxRuntime ends tasks the model forgot. Their completion is still
	// reported like any other.
	taskMaxRuntime     = 10 * time.Hour
	taskTerminateGrace = 2 * time.Second
	taskPreviewBytes   = 2 << 10
	taskPollInterval   = time.Second
)

var (
	errTaskUnknown = errors.New("unknown task; background tasks end when AICE exits or the Session changes")
	errTasksClosed = errors.New("background tasks are closed")
	// ErrTaskClosed refuses to continue a run task stopped with task_kill.
	ErrTaskClosed = errors.New("it was stopped with task_kill and cannot continue")
)

// Task kinds.
const (
	TaskBash  = "bash"
	TaskAgent = "agent"
)

// Tasks owns the background tasks of one run environment: shell processes
// and in-process runs such as subagents, their output files and unreported
// completions. Tasks outlive the tool
// call and run that started them; Close or KillAll ends them. Each task has
// one supervising goroutine, owned and awaited by Tasks.
type Tasks struct {
	dir string
	// life is canceled by Close; supervisors terminate their task on it.
	life context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu     sync.Mutex
	tasks  map[string]*task
	order  []string
	closed bool
	// finished holds completions in the order they happened until they are
	// reported through a notice, task_output or task_kill.
	finished []*task
	// changed is closed and replaced when a task finishes.
	changed chan struct{}
}

type task struct {
	id          string
	kind        string
	command     string
	description string
	path        string
	started     time.Time
	stopReq     chan string
	done        chan struct{}

	// Written by the supervisor before done closes; read after done.
	exitCode int
	reason   string
	// failure is why a run task returned an error on its own.
	failure    string
	delegation *llm.ToolDelegation
	// reported is guarded by Tasks.mu.
	reported bool
	// owner is the state its starter keeps with a run task, kept across
	// continuations. closed, guarded by Tasks.mu, refuses them after
	// task_kill.
	owner  any
	closed bool
}

// TaskStatus is a snapshot of one task.
type TaskStatus struct {
	ID          string
	Kind        string
	Command     string
	Description string
	OutputPath  string
	Running     bool
	// ExitCode is meaningful when Reason is empty; Reason names why AICE
	// terminated the task.
	ExitCode int
	Reason   string
	// Failure is why a run task failed on its own.
	Failure string
	// Delegation records a finished subagent task.
	Delegation *llm.ToolDelegation
	// FirstReport marks the one snapshot that reports this completion; its
	// Delegation must be recorded with that report.
	FirstReport bool
}

// TaskRun is a background task that is not a process, identified by id. It
// writes its output to out and returns when done or when ctx ends; a
// delegation it returns is reported with its completion.
type TaskRun func(ctx context.Context, id string, out io.Writer) (*llm.ToolDelegation, error)

// NewTasks creates the private output directory for background tasks.
func NewTasks() (*Tasks, error) {
	dir, err := os.MkdirTemp("", "aice-tasks-")
	if err != nil {
		return nil, fmt.Errorf("tool: create task output directory: %w", err)
	}
	life, stop := context.WithCancel(context.Background())
	return &Tasks{dir: dir, life: life, stop: stop, tasks: make(map[string]*task), changed: make(chan struct{})}, nil
}

// Dir is the directory holding task output files. Tools may read it; nothing
// else writes there.
func (t *Tasks) Dir() string { return t.dir }

// reserveLocked admits one task and creates its output file. The caller
// holds Tasks.mu and registers the task only once it has started.
func (t *Tasks) reserveLocked(kind, command, description string) (*task, *os.File, error) {
	if t.closed {
		return nil, nil, errTasksClosed
	}
	if t.runningLocked() >= maxRunningTasks {
		return nil, nil, fmt.Errorf("at most %d background tasks may run at once; stop one with task_kill", maxRunningTasks)
	}
	if !t.evictLocked() {
		return nil, nil, fmt.Errorf("at most %d background tasks are retained; read finished ones with task_output or stop running ones first", maxRetainedTasks)
	}
	id, err := newTaskID()
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(t.dir, id+".log")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create task output: %w", err)
	}
	return &task{
		id: id, kind: kind, command: command, description: description, path: path,
		started: time.Now(), stopReq: make(chan string, 1), done: make(chan struct{}),
	}, output, nil
}

func (t *Tasks) registerLocked(current *task) {
	t.tasks[current.id] = current
	t.order = append(t.order, current.id)
}

// start launches command with the shell and records it as a task.
func (t *Tasks) start(shellPath, dir, command, description string) (TaskStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current, output, err := t.reserveLocked(TaskBash, command, description)
	if err != nil {
		return TaskStatus{}, err
	}
	defer output.Close()
	path := current.path

	processCtx, kill := context.WithCancel(context.Background())
	// Raw shell text intentionally reaches bash at this tool boundary. The
	// file is the child's stdout, so no copy goroutine ties Wait to
	// descendants that keep it open.
	cmd := exec.CommandContext(processCtx, shellPath, "--noprofile", "--norc", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	configureProcess(cmd)
	cleanup, err := startProcessTree(cmd)
	if err != nil {
		kill()
		_ = os.Remove(path)
		return TaskStatus{}, fmt.Errorf("start command: %w", err)
	}
	t.registerLocked(current)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer cleanup()
		defer kill()
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		err := t.supervise(current, waited, func() error { return terminateTask(cmd, kill, waited) })
		current.exitCode = exitCode(err)
		t.finish(current)
	}()
	return current.status(), nil
}

// StartRun runs fn as a background task labeled by description, keeping owner
// with it for Owner. Stopping the task cancels fn's context and waits for it
// to return.
func (t *Tasks) StartRun(kind, description string, owner any, fn TaskRun) (TaskStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.admitAgentLocked(kind); err != nil {
		return TaskStatus{}, err
	}
	current, output, err := t.reserveLocked(kind, "", description)
	if err != nil {
		return TaskStatus{}, err
	}
	current.owner = owner
	t.registerLocked(current)
	t.runLocked(current, output, fn)
	return current.status(), nil
}

func (t *Tasks) admitAgentLocked(kind string) error {
	if kind == TaskAgent && t.runningKindLocked(TaskAgent) >= maxRunningAgents {
		return fmt.Errorf("at most %d background subagents may run at once; wait for one or stop it with task_kill", maxRunningAgents)
	}
	return nil
}

// TaskContinuation is a finished run task started again under its ID.
type TaskContinuation struct {
	Task TaskStatus
	// Unseen is the previous run when no result had reported it, with its
	// report rendered before the new run rewrote the output. Its delegation
	// must be recorded with the result that reports it.
	Unseen       *TaskStatus
	UnseenReport string
}

// Continue runs fn again as the finished run task id, with the same ID,
// description, owner and output file, which the new run rewrites. A task
// stopped with task_kill is closed and cannot continue.
func (t *Tasks) Continue(id string, fn TaskRun) (TaskContinuation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, ok := t.tasks[id]
	switch {
	case t.closed:
		return TaskContinuation{}, errTasksClosed
	case !ok:
		return TaskContinuation{}, fmt.Errorf("%s: %w", id, errTaskUnknown)
	case previous.kind == TaskBash:
		return TaskContinuation{}, fmt.Errorf("%s is a command, not a run that can continue", id)
	case previous.status().Running:
		return TaskContinuation{}, fmt.Errorf("%s is still running", id)
	case previous.closed:
		return TaskContinuation{}, fmt.Errorf("%s: %w", id, ErrTaskClosed)
	case t.runningLocked() >= maxRunningTasks:
		return TaskContinuation{}, fmt.Errorf("at most %d background tasks may run at once; stop one with task_kill", maxRunningTasks)
	}
	if err := t.admitAgentLocked(previous.kind); err != nil {
		return TaskContinuation{}, err
	}
	// The previous report is rendered before the new run truncates it, and
	// reported only once the new run can start.
	var continuation TaskContinuation
	if status := previous.status(); !previous.reported {
		continuation.UnseenReport = taskReport(status, agentNoticeBytes)
	}
	output, err := os.OpenFile(previous.path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return TaskContinuation{}, fmt.Errorf("reopen task output: %w", err)
	}
	if status := previous.report(); status.FirstReport {
		continuation.Unseen = &status
	}
	current := &task{
		id: id, kind: previous.kind, description: previous.description, path: previous.path, owner: previous.owner,
		started: time.Now(), stopReq: make(chan string, 1), done: make(chan struct{}),
	}
	t.tasks[id] = current
	// A continued task is the most recent, so eviction reaches it last.
	t.order = append(slices.DeleteFunc(t.order, func(other string) bool { return other == id }), id)
	t.runLocked(current, output, fn)
	continuation.Task = current.status()
	return continuation, nil
}

// Owner returns the state the starter of run task id keeps with it.
func (t *Tasks) Owner(id string) (any, error) {
	tasks, err := t.lookup([]string{id})
	if err != nil {
		return nil, err
	}
	return tasks[0].owner, nil
}

// Done returns a channel closed when task id's current run finishes, without
// reporting it.
func (t *Tasks) Done(id string) (<-chan struct{}, error) {
	tasks, err := t.lookup([]string{id})
	if err != nil {
		return nil, err
	}
	return tasks[0].done, nil
}

// List snapshots every retained task in start order, without reporting any.
func (t *Tasks) List() []TaskStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	statuses := make([]TaskStatus, 0, len(t.order))
	for _, id := range t.order {
		statuses = append(statuses, t.tasks[id].status())
	}
	return statuses
}

// runLocked starts fn's supervised run; the caller holds Tasks.mu.
func (t *Tasks) runLocked(current *task, output *os.File, fn TaskRun) {
	ctx, cancel := context.WithCancel(context.Background())
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		waited := make(chan error, 1)
		go func() {
			delegation, err := fn(ctx, current.id, output)
			current.delegation = delegation
			waited <- err
		}()
		err := t.supervise(current, waited, func() error { cancel(); return <-waited })
		cancel()
		_ = output.Close()
		if err != nil && current.reason == "" {
			current.failure = err.Error()
		}
		t.finish(current)
	}()
}

// finish publishes a completion. Under Tasks.mu a task stops running exactly
// when it joins finished, so a waiter never sees it neither running nor
// reportable.
func (t *Tasks) finish(current *task) {
	t.mu.Lock()
	defer t.mu.Unlock()
	close(current.done)
	t.finished = append(t.finished, current)
	close(t.changed)
	t.changed = make(chan struct{})
}

// Changed returns a channel closed when a task next finishes. Take it before
// checking state, so a completion in between is not missed.
func (t *Tasks) Changed() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.changed
}

// supervise waits for the task and terminates it on request, on Close, after
// the maximum runtime or when its output exceeds the limit. It records why
// AICE ended the task and returns the task's own result.
func (t *Tasks) supervise(current *task, waited <-chan error, terminate func() error) error {
	poll := time.NewTicker(taskPollInterval)
	defer poll.Stop()
	deadline := time.NewTimer(taskMaxRuntime)
	defer deadline.Stop()

	var err error
	reason := ""
loop:
	for {
		select {
		case err = <-waited:
			break loop
		case reason = <-current.stopReq:
		case <-t.life.Done():
			reason = "AICE closed"
		case <-deadline.C:
			reason = fmt.Sprintf("exceeded maximum runtime %s", taskMaxRuntime)
		case <-poll.C:
			if info, statErr := os.Stat(current.path); statErr == nil && info.Size() > taskOutputLimit {
				reason = fmt.Sprintf("output exceeded %d MiB", taskOutputLimit>>20)
			}
		}
		if reason != "" {
			err = terminate()
			break
		}
	}
	current.reason = reason
	return err
}

// terminateTask asks the process tree to stop, then kills it after a grace.
func terminateTask(cmd *exec.Cmd, kill context.CancelFunc, waited <-chan error) error {
	if interruptProcessTree(cmd) == nil {
		timer := time.NewTimer(taskTerminateGrace)
		defer timer.Stop()
		select {
		case err := <-waited:
			return err
		case <-timer.C:
		}
	}
	kill()
	return <-waited
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// status returns a snapshot; the caller holds Tasks.mu or owns the task.
func (c *task) status() TaskStatus {
	status := TaskStatus{
		ID: c.id, Kind: c.kind, Command: c.command, Description: c.description,
		OutputPath: c.path, Running: true,
	}
	select {
	case <-c.done:
		status.Running = false
		status.ExitCode, status.Reason, status.Failure = c.exitCode, c.reason, c.failure
		if c.delegation != nil {
			delegation := *c.delegation
			status.Delegation = &delegation
		}
	default:
	}
	return status
}

// report snapshots a task for a caller that reports it; the caller holds
// Tasks.mu. A finished task's first report is marked.
func (c *task) report() TaskStatus {
	status := c.status()
	if !status.Running && !c.reported {
		c.reported, status.FirstReport = true, true
	}
	return status
}

// Wait blocks until every listed task finishes, wait elapses or ctx ends, then
// returns their snapshots. Finished tasks count as reported.
func (t *Tasks) Wait(ctx context.Context, ids []string, wait time.Duration) ([]TaskStatus, error) {
	tasks, err := t.lookup(ids)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
waiting:
	for _, current := range tasks {
		select {
		case <-current.done:
		case <-timer.C:
			break waiting
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	statuses := make([]TaskStatus, len(tasks))
	for index, current := range tasks {
		statuses[index] = current.report()
	}
	return statuses, nil
}

// Kill terminates one task and waits for it to end. Its completion is reported
// by the caller's result instead of a later notice.
func (t *Tasks) Kill(ctx context.Context, id string) (TaskStatus, error) {
	tasks, err := t.lookup([]string{id})
	if err != nil {
		return TaskStatus{}, err
	}
	current := tasks[0]
	t.mu.Lock()
	current.closed = true
	t.mu.Unlock()
	select {
	case current.stopReq <- "killed by task_kill":
	default:
	}
	select {
	case <-current.done:
	case <-ctx.Done():
		return TaskStatus{}, ctx.Err()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return current.report(), nil
}

// Stop asks one task to end for reason, as a person does from a frontend,
// without waiting for it. Unlike Kill its completion is reported as usual, so
// the agent learns why it ended, and a run task may still be continued.
func (t *Tasks) Stop(id, reason string) error {
	tasks, err := t.lookup([]string{id})
	if err != nil {
		return err
	}
	select {
	case tasks[0].stopReq <- reason:
	default:
	}
	return nil
}

// Running reports how many tasks are still running.
func (t *Tasks) Running() int {
	return t.RunningKind("")
}

// RunningKind reports how many tasks of kind, or of every kind for "", are
// still running.
func (t *Tasks) RunningKind(kind string) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runningKindLocked(kind)
}

// HasCompletions reports whether a finished task waits to be reported.
func (t *Tasks) HasCompletions() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, current := range t.finished {
		if !current.reported {
			return true
		}
	}
	return false
}

// TakeCompletions returns finished tasks that no result has reported yet, in
// completion order, and marks them reported.
func (t *Tasks) TakeCompletions() []TaskStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	var statuses []TaskStatus
	for _, current := range t.finished {
		if !current.reported {
			statuses = append(statuses, current.report())
		}
	}
	t.finished = nil
	return statuses
}

// KillAll terminates every task and deletes their output, leaving Tasks
// usable. A new Session starts without tasks it cannot know about. It returns
// the notification of the subagents no result reported yet, stopped now or
// finished before, so the Session that started them can still record their
// reports and usage; ok is false when there is none. Commands are not
// reported: their output means nothing once they are gone. A nil Tasks has
// nothing to stop.
func (t *Tasks) KillAll() (notice llm.UserMessage, ok bool) {
	if t == nil {
		return llm.UserMessage{}, false
	}
	t.mu.Lock()
	tasks := make([]*task, 0, len(t.tasks))
	var unreported []*task
	for _, current := range t.tasks {
		if current.kind == TaskAgent && !current.reported {
			unreported = append(unreported, current)
		}
		// No later notification reports them to the next Session.
		current.reported = true
		tasks = append(tasks, current)
	}
	clear(t.tasks)
	t.order, t.finished = nil, nil
	t.mu.Unlock()
	for _, current := range tasks {
		select {
		case current.stopReq <- "Session changed":
		default:
		}
	}
	for _, current := range tasks {
		<-current.done
	}
	// Rendered before the output is removed: the notice carries the reports.
	if len(unreported) > 0 {
		slices.SortFunc(unreported, func(a, b *task) int { return a.started.Compare(b.started) })
		statuses := make([]TaskStatus, 0, len(unreported))
		for _, current := range unreported {
			statuses = append(statuses, current.status())
		}
		notice, _ = renderNotice(statuses, 0)
		ok = true
	}
	for _, current := range tasks {
		_ = os.Remove(current.path)
	}
	return notice, ok
}

// Close terminates every task, waits for their supervisors and removes the
// output directory. Later starts fail. A nil Tasks closes trivially.
func (t *Tasks) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.stop()
	t.wg.Wait()
	if err := os.RemoveAll(t.dir); err != nil {
		return fmt.Errorf("tool: remove task output: %w", err)
	}
	return nil
}

func (t *Tasks) lookup(ids []string) ([]*task, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tasks := make([]*task, 0, len(ids))
	for _, id := range ids {
		current, ok := t.tasks[id]
		if !ok {
			return nil, fmt.Errorf("%s: %w", id, errTaskUnknown)
		}
		tasks = append(tasks, current)
	}
	return tasks, nil
}

func (t *Tasks) runningLocked() int {
	return t.runningKindLocked("")
}

// runningKindLocked counts running tasks of kind, or of every kind for "".
func (t *Tasks) runningKindLocked(kind string) int {
	running := 0
	for _, current := range t.tasks {
		if (kind == "" || current.kind == kind) && current.status().Running {
			running++
		}
	}
	return running
}

// evictLocked frees one registry slot, dropping the oldest reported task. It
// reports false when every retained task still runs or waits to be reported.
func (t *Tasks) evictLocked() bool {
	if len(t.tasks) < maxRetainedTasks {
		return true
	}
	for index, id := range t.order {
		current := t.tasks[id]
		if current.status().Running || !current.reported {
			continue
		}
		delete(t.tasks, id)
		t.order = slices.Delete(t.order, index, index+1)
		_ = os.Remove(current.path)
		return true
	}
	return false
}

func newTaskID() (string, error) {
	var value [3]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	return "bg-" + hex.EncodeToString(value[:]), nil
}

// outputTail returns at most limit trailing bytes of a task's output, starting
// at a rune boundary.
func outputTail(path string, limit int64) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	offset := max(0, info.Size()-limit)
	data, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return "", false, err
	}
	text := strings.ToValidUTF8(string(data), "")
	return text, offset > 0, nil
}

// outputHead returns at most limit leading bytes of a task's output, ending at
// a rune boundary.
func outputHead(path string, limit int64) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return strings.ToValidUTF8(string(data), ""), truncated, nil
}
