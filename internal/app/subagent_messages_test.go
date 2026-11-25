package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// messagingModel drives a parent that starts one background explore child
// and messages it, and a child scripted by its own turns. parent decides the
// parent's next step from the last message it received.
type messagingModel struct {
	mu       sync.Mutex
	id       string
	children []llm.Request
	child    func(ctx context.Context, request llm.Request, turn int) (llm.Stream, error)
	parent   func(m *messagingModel, request llm.Request, last llm.AgentMessage) llm.Stream
}

var taskIDPattern = regexp.MustCompile(`bg-[0-9a-f]{6}`)

func (m *messagingModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if strings.Contains(request.SystemPrompt, "# Subagent") {
		m.mu.Lock()
		m.children = append(m.children, request)
		turn := len(m.children)
		m.mu.Unlock()
		return m.child(ctx, request, turn)
	}
	last := request.Messages[len(request.Messages)-1]
	if result, ok := last.(llm.ToolResultMessage); ok && result.ToolName == "agent" {
		m.mu.Lock()
		m.id = taskIDPattern.FindString(result.Content[0].Text)
		m.mu.Unlock()
	}
	return m.parent(m, request, last), nil
}

func (m *messagingModel) call(request llm.Request, id, name string, arguments any) llm.Stream {
	data, _ := json.Marshal(arguments)
	return toolCallEventStream(request.Model, llm.ToolCall{ID: id, Name: name, Arguments: data})
}

func (m *messagingModel) taskID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.id
}

func noticeText(message llm.AgentMessage) string {
	if user, ok := message.(llm.UserMessage); ok && user.IsNotice() {
		return user.Content[0].Text
	}
	return ""
}

func userTexts(request llm.Request) []string {
	var texts []string
	for _, message := range request.Messages {
		if user, ok := message.(llm.UserMessage); ok {
			texts = append(texts, user.Content[0].Text)
		}
	}
	return texts
}

func runPrintWith(t *testing.T, model agent.Model) string {
	t.Helper()
	return runPrintWithConfig(t, config.Config{APIKeys: map[string]string{"deepseek": "test-key"}}, model)
}

func runPrintWithConfig(t *testing.T, cfg config.Config, model agent.Model) string {
	t.Helper()
	return runPrintIn(t, cfg, model, t.TempDir())
}

// runPrintIn runs --print in workspace, with extra flags before it.
func runPrintIn(t *testing.T, cfg config.Config, model agent.Model, workspace string, extra ...string) string {
	t.Helper()
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return cfg, nil
		},
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		userHomeDir: func() (string, error) { return t.TempDir(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs(append(append([]string{"--workspace", workspace}, extra...), "--print", "investigate"))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	return output.String()
}

// A finished child continues with send_message: same task, its whole
// conversation, and a new report as a notification.
func TestSendMessageContinuesFinishedSubagent(t *testing.T) {
	t.Parallel()
	model := &messagingModel{
		child: func(_ context.Context, request llm.Request, turn int) (llm.Stream, error) {
			return textEventStream(request.Model, map[int]string{1: "first report", 2: "second report"}[turn], llm.Usage{}, llm.StopReasonStop), nil
		},
		parent: func(m *messagingModel, request llm.Request, last llm.AgentMessage) llm.Stream {
			switch notice := noticeText(last); {
			case strings.Contains(notice, "second report"):
				return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop)
			case strings.Contains(notice, "first report"):
				return m.call(request, "more", "send_message", map[string]any{"to": m.taskID(), "message": "dig deeper"})
			case len(request.Messages) == 1:
				return m.call(request, "start", "agent", map[string]any{"description": "probe", "prompt": "look around", "subagent_type": "explore"})
			}
			return textEventStream(request.Model, "waiting", llm.Usage{}, llm.StopReasonStop)
		},
	}
	if output := runPrintWith(t, model); !strings.HasSuffix(output, "parent done\n") {
		t.Fatalf("output = %q", output)
	}
	if len(model.children) != 2 {
		t.Fatalf("child requests = %d", len(model.children))
	}
	continued := model.children[1]
	if texts := userTexts(continued); len(texts) != 2 || texts[0] != "look around" || texts[1] != "dig deeper" {
		t.Fatalf("continued child saw users %q", texts)
	}
	if reply, ok := continued.Messages[1].(llm.AssistantMessage); !ok || visibleText(reply.Content, "") != "first report" {
		t.Fatalf("continued child lost its first turn: %#v", continued.Messages)
	}
}

// A message to a running child reaches it within the same task run: when it
// would stop, it takes the message and goes on.
func TestSendMessageSteersRunningSubagent(t *testing.T) {
	t.Parallel()
	sent := make(chan struct{})
	model := &messagingModel{
		child: func(ctx context.Context, request llm.Request, turn int) (llm.Stream, error) {
			if turn == 1 {
				select {
				case <-sent:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return textEventStream(request.Model, "partial", llm.Usage{}, llm.StopReasonStop), nil
			}
			return textEventStream(request.Model, "final with the extra check", llm.Usage{}, llm.StopReasonStop), nil
		},
		parent: func(m *messagingModel, request llm.Request, last llm.AgentMessage) llm.Stream {
			if strings.Contains(noticeText(last), "final with the extra check") {
				return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop)
			}
			if result, ok := last.(llm.ToolResultMessage); ok {
				switch result.ToolName {
				case "agent":
					return m.call(request, "steer", "send_message", map[string]any{"to": m.taskID(), "message": "also check the cache"})
				case "send_message":
					close(sent)
				}
			}
			if len(request.Messages) == 1 {
				return m.call(request, "start", "agent", map[string]any{"description": "probe", "prompt": "look around", "subagent_type": "explore"})
			}
			return textEventStream(request.Model, "waiting", llm.Usage{}, llm.StopReasonStop)
		},
	}
	if output := runPrintWith(t, model); !strings.HasSuffix(output, "parent done\n") {
		t.Fatalf("output = %q", output)
	}
	if len(model.children) != 2 {
		t.Fatalf("child requests = %d", len(model.children))
	}
	if texts := userTexts(model.children[1]); len(texts) != 2 || texts[1] != "also check the cache" {
		t.Fatalf("steered child saw users %q", texts)
	}
}

// An interrupt cancels the child's current step; it continues with the
// message on its repaired conversation.
func TestSendMessageInterruptsRunningSubagent(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	var once sync.Once
	var interrupted string
	model := &messagingModel{
		child: func(ctx context.Context, request llm.Request, turn int) (llm.Stream, error) {
			if turn == 1 {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return textEventStream(request.Model, "redirected report", llm.Usage{}, llm.StopReasonStop), nil
		},
		parent: func(m *messagingModel, request llm.Request, last llm.AgentMessage) llm.Stream {
			if strings.Contains(noticeText(last), "redirected report") {
				return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop)
			}
			if result, ok := last.(llm.ToolResultMessage); ok {
				switch result.ToolName {
				case "agent":
					<-started
					return m.call(request, "stop", "send_message", map[string]any{"to": m.taskID(), "message": "stop that; check the cache", "interrupt": true})
				case "send_message":
					interrupted = result.Content[0].Text
				}
			}
			if len(request.Messages) == 1 {
				return m.call(request, "start", "agent", map[string]any{"description": "probe", "prompt": "look around", "subagent_type": "explore"})
			}
			return textEventStream(request.Model, "waiting", llm.Usage{}, llm.StopReasonStop)
		},
	}
	if output := runPrintWith(t, model); !strings.HasSuffix(output, "parent done\n") {
		t.Fatalf("output = %q", output)
	}
	if !strings.HasPrefix(interrupted, "Interrupted subagent") {
		t.Fatalf("send_message result = %q", interrupted)
	}
	if texts := userTexts(model.children[len(model.children)-1]); len(texts) != 2 || texts[1] != "stop that; check the cache" {
		t.Fatalf("interrupted child saw users %q", texts)
	}
}

// A child that failed before its Session existed has nothing to continue.
func TestSendMessageRefusesChildThatNeverStarted(t *testing.T) {
	t.Parallel()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	child := &subagent{}
	status, err := tasks.StartRun(tool.TaskAgent, "probe", child, func(context.Context, string, io.Writer) (*llm.ToolDelegation, error) {
		return nil, errors.New("worktree unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	done, _ := tasks.Done(status.ID)
	<-done
	_, err = send(t.Context(), tasks, tool.MessageRequest{To: status.ID, Message: "retry"})
	if err == nil || !strings.Contains(err.Error(), "failed before it started") || child.running {
		t.Fatalf("send = %v, running %v", err, child.running)
	}
}
