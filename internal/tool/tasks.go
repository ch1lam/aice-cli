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
)

const (
	maxRunningTasks  = 8
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
)

// Tasks owns the background shell tasks of one run environment: their
// processes, output files and unreported completions. Tasks outlive the tool
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
}

type task struct {
	id          string
	command     string
	description string
	path        string
	started     time.Time
	stopReq     chan string
	done        chan struct{}

	// Written by the supervisor before done closes; read after done.
	exitCode int
	reason   string
	// reported is guarded by Tasks.mu.
	reported bool
}

// TaskStatus is a snapshot of one task.
type TaskStatus struct {
	ID          string
	Command     string
	Description string
	OutputPath  string
	Running     bool
	// ExitCode is meaningful when Reason is empty; Reason names why AICE
	// terminated the task.
	ExitCode int
	Reason   string
}

// NewTasks creates the private output directory for background tasks.
func NewTasks() (*Tasks, error) {
	dir, err := os.MkdirTemp("", "aice-tasks-")
	if err != nil {
		return nil, fmt.Errorf("tool: create task output directory: %w", err)
	}
	life, stop := context.WithCancel(context.Background())
	return &Tasks{dir: dir, life: life, stop: stop, tasks: make(map[string]*task)}, nil
}

// Dir is the directory holding task output files. Tools may read it; nothing
// else writes there.
func (t *Tasks) Dir() string { return t.dir }

// start launches command with the shell and records it as a task.
func (t *Tasks) start(shellPath, dir, command, description string) (TaskStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return TaskStatus{}, errTasksClosed
	}
	if t.runningLocked() >= maxRunningTasks {
		return TaskStatus{}, fmt.Errorf("at most %d background tasks may run at once; stop one with task_kill", maxRunningTasks)
	}
	if !t.evictLocked() {
		return TaskStatus{}, fmt.Errorf("at most %d background tasks are retained; read or stop running tasks first", maxRetainedTasks)
	}
	id, err := newTaskID()
	if err != nil {
		return TaskStatus{}, err
	}
	path := filepath.Join(t.dir, id+".log")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return TaskStatus{}, fmt.Errorf("create task output: %w", err)
	}
	defer output.Close()

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
	current := &task{
		id: id, command: command, description: description, path: path,
		started: time.Now(), stopReq: make(chan string, 1), done: make(chan struct{}),
	}
	t.tasks[id] = current
	t.order = append(t.order, id)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer cleanup()
		defer kill()
		t.supervise(current, cmd, kill)
	}()
	return current.status(), nil
}

// supervise waits for the task and terminates it on request, on Close, after
// the maximum runtime or when its output exceeds the limit.
func (t *Tasks) supervise(current *task, cmd *exec.Cmd, kill context.CancelFunc) {
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
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
			err = terminateTask(cmd, kill, waited)
			break
		}
	}

	current.reason = reason
	current.exitCode = exitCode(err)
	t.mu.Lock()
	t.finished = append(t.finished, current)
	t.mu.Unlock()
	close(current.done)
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
		ID: c.id, Command: c.command, Description: c.description,
		OutputPath: c.path, Running: true,
	}
	select {
	case <-c.done:
		status.Running = false
		status.ExitCode, status.Reason = c.exitCode, c.reason
	default:
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
		statuses[index] = current.status()
		if !statuses[index].Running {
			current.reported = true
		}
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
	current.reported = true
	return current.status(), nil
}

// Running reports how many tasks are still running.
func (t *Tasks) Running() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runningLocked()
}

// TakeCompletions returns finished tasks that no result has reported yet, in
// completion order, and marks them reported.
func (t *Tasks) TakeCompletions() []TaskStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	var statuses []TaskStatus
	for _, current := range t.finished {
		if !current.reported {
			current.reported = true
			statuses = append(statuses, current.status())
		}
	}
	t.finished = nil
	return statuses
}

// KillAll terminates every task and deletes their output, leaving Tasks
// usable. A new Session starts without tasks it cannot know about.
func (t *Tasks) KillAll() {
	t.mu.Lock()
	tasks := make([]*task, 0, len(t.tasks))
	for _, current := range t.tasks {
		// Their completions belong to the Session that started them.
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
		_ = os.Remove(current.path)
	}
}

// Close terminates every task, waits for their supervisors and removes the
// output directory. Later starts fail.
func (t *Tasks) Close() error {
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
	running := 0
	for _, current := range t.tasks {
		if current.status().Running {
			running++
		}
	}
	return running
}

// evictLocked frees one registry slot, dropping the oldest finished task. It
// reports false when every retained task is still running.
func (t *Tasks) evictLocked() bool {
	if len(t.tasks) < maxRetainedTasks {
		return true
	}
	for index, id := range t.order {
		current := t.tasks[id]
		if current.status().Running {
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
