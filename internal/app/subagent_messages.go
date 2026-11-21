package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// subagent is a background child's state, kept with its task so the parent
// can message it while it runs and continue it once it finished. Its task
// ID names it; task_kill closes it.
type subagent struct {
	request tool.DelegateRequest
	tree    *worktree

	mu sync.Mutex
	// path is the child's Session, which every turn continues.
	path string
	// queue holds the parent's messages until the child takes them.
	queue []string
	// running covers a task run from its start until it stops taking
	// messages; interrupt cancels the current turn, and interrupted keeps
	// the cancelled turn from taking the messages the next one starts with.
	running     bool
	interrupt   context.CancelFunc
	interrupted bool
}

func (s *subagent) sessionPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

func (s *subagent) setSessionPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
}

// takeMessage is the child Loop's steering and follow-up source: the
// parent's waiting messages as one user message.
func (s *subagent) takeMessage() (agent.InputMessage, bool, error) {
	s.mu.Lock()
	var text string
	if !s.interrupted {
		text = strings.Join(s.queue, "\n\n")
		s.queue = nil
	}
	s.mu.Unlock()
	if text == "" {
		return agent.InputMessage{}, false, nil
	}
	return messageInput(text)
}

func messageInput(text string) (agent.InputMessage, bool, error) {
	message, err := llm.NewUserMessage(llm.NewTextContent(text).Part())
	if err != nil {
		return agent.InputMessage{}, false, err
	}
	id, err := session.NewID()
	if err != nil {
		return agent.InputMessage{}, false, err
	}
	return agent.InputMessage{ID: id, Message: message}, true, nil
}

// beginTurn lets an interrupt cancel the turn about to run.
func (s *subagent) beginTurn(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupt, s.interrupted = cancel, false
}

// endTurn returns the messages that arrived after the child's Loop last
// looked, to run another turn with; otherwise the child stops running, and
// a later message continues it with a new task run.
func (s *subagent) endTurn(ctx context.Context) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupt = nil
	if ctx.Err() == nil && len(s.queue) > 0 {
		text := strings.Join(s.queue, "\n\n")
		s.queue = nil
		return text, true
	}
	s.queue, s.running = nil, false
	return "", false
}

// stop ends a run that could not take its turns; waiting messages are
// dropped with it.
func (s *subagent) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue, s.running, s.interrupt, s.interrupted = nil, false, nil, false
}

// background is one task run of a background child: turns until no message
// waits. Its report, the last turn's, goes to the task's output file and its
// delegation record, covering every turn, to whichever result first reports
// the completion. A writer gets its own artifact directory; one sharing the
// workspace is named by its task ID for file claims.
func (d *delegation) background(values context.Context, child *subagent, prompt string) tool.TaskRun {
	request := child.request
	return func(taskCtx context.Context, id string, out io.Writer) (*llm.ToolDelegation, error) {
		ctx, cancel := context.WithCancel(values)
		defer cancel()
		defer context.AfterFunc(taskCtx, cancel)()
		// A run that fails before its turns end stops taking messages.
		settled := false
		defer func() {
			if !settled {
				child.stop()
			}
		}()
		run := childRun{background: true, agent: child, tree: child.tree, label: id + " · " + request.Description}
		if tree := child.tree; tree != nil {
			if err := tree.reopen(ctx); err != nil {
				return nil, err
			}
			// Settle the worktree however the run ends; its note follows the report.
			defer func() { fmt.Fprintln(out, tree.finish(ctx, request.Description)) }()
		}
		if !request.ReadOnly {
			if run.tree == nil {
				d.guard.claims.begin(id)
				defer d.guard.claims.end(id)
				ctx = withWriter(ctx, id)
			}
			run.artifacts = filepath.Join(d.workspace.Path(), ".aice", "agents", id)
			if err := os.MkdirAll(run.artifacts, 0o700); err != nil {
				return nil, fmt.Errorf("create artifact directory: %w", err)
			}
		}
		var res tool.DelegateResult
		total := llm.ToolDelegation{}
		var elapsed time.Duration
		for text, more := prompt, true; more; {
			turnCtx, stopTurn := context.WithCancel(ctx)
			child.beginTurn(stopTurn)
			res = d.run(turnCtx, request, run, text)
			stopTurn()
			total.Usage = llm.AddUsage(total.Usage, res.Usage)
			total.Turns += res.Turns
			total.ToolCalls += res.ToolCalls
			elapsed += res.Duration
			text, more = child.endTurn(ctx)
		}
		settled = true
		total.SessionID = res.SessionID
		report := tool.TruncateAgentReport(res.Report)
		if report == "" {
			report = "(the subagent finished without a report)"
		}
		fmt.Fprintf(out, "%s\n[subagent: %d turns, %d tool calls, %s", report, total.Turns, total.ToolCalls, elapsed.Round(time.Second))
		if res.Transcript != "" {
			fmt.Fprintf(out, "; transcript: %s", res.Transcript)
		}
		if artifacts := run.artifacts; artifacts != "" {
			if rel, err := filepath.Rel(d.workspace.Path(), artifacts); err == nil {
				artifacts = rel
			}
			fmt.Fprintf(out, "; artifacts: %s", artifacts)
		}
		fmt.Fprintln(out, "]")
		return &total, res.Err
	}
}

// messageRouter is the send_message tool's Messenger: it reaches the
// children of the main run that made the call.
type messageRouter struct{}

func (messageRouter) Send(ctx context.Context, request tool.MessageRequest) (tool.MessageResult, error) {
	d, _ := ctx.Value(delegationKey{}).(*delegation)
	if d == nil || d.tasks == nil {
		return tool.MessageResult{}, errNoDelegation
	}
	return d.send(ctx, request)
}

// send queues a message for a running child, interrupting its turn when
// asked, or continues a finished child with it as a new task run.
func (d *delegation) send(ctx context.Context, request tool.MessageRequest) (tool.MessageResult, error) {
	owner, err := d.tasks.Owner(request.To)
	if err != nil {
		return tool.MessageResult{}, err
	}
	child, ok := owner.(*subagent)
	if !ok {
		return tool.MessageResult{}, fmt.Errorf("%s is not a subagent", request.To)
	}
	child.mu.Lock()
	if child.running {
		child.queue = append(child.queue, request.Message)
		text := fmt.Sprintf("Delivered to running subagent %s; it reads the message at its next step.", request.To)
		if request.Interrupt && child.interrupt != nil {
			child.interrupt()
			child.interrupted = true
			text = fmt.Sprintf("Interrupted subagent %s; it continues with the message now.", request.To)
		}
		child.mu.Unlock()
		return tool.MessageResult{Text: text + " Its report arrives as a notification."}, nil
	}
	child.running = true
	child.mu.Unlock()
	continuation, err := d.continueChild(ctx, child, request)
	if err != nil {
		child.mu.Lock()
		child.queue, child.running = nil, false
		child.mu.Unlock()
		return tool.MessageResult{}, err
	}
	text := fmt.Sprintf("Subagent %s continues in the background with your message and its whole conversation; "+
		"its new report arrives as a notification, also after you end your turn.", request.To)
	result := tool.MessageResult{Text: text}
	if unseen := continuation.Unseen; unseen != nil {
		result.Text = "Its previous report, which you had not received yet:\n" + continuation.UnseenReport + "\n\n" + text
		if unseen.Delegation != nil {
			result.Details = &llm.ToolResultDetails{State: llm.ExecutionReturned, Delegations: []llm.ToolDelegation{*unseen.Delegation}}
		}
	}
	return result, nil
}

// continueChild starts a finished child's next task run once its last run
// has published its end.
func (d *delegation) continueChild(ctx context.Context, child *subagent, request tool.MessageRequest) (tool.TaskContinuation, error) {
	done, err := d.tasks.Done(request.To)
	if err != nil {
		return tool.TaskContinuation{}, err
	}
	select {
	case <-done:
	case <-ctx.Done():
		return tool.TaskContinuation{}, ctx.Err()
	}
	return d.tasks.Continue(request.To, d.background(context.WithoutCancel(ctx), child, request.Message))
}
