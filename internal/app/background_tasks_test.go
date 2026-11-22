package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// A background task outlives its call, its completion reaches the model as a
// notification after the next tool round, its output file is readable
// without a Guard ask (which --print would refuse), and the run's end
// removes it.
func TestApplicationPrintReportsBackgroundTaskCompletion(t *testing.T) {
	t.Parallel()
	if _, err := deps.FindNativeBash(deps.DefaultOptions()); err != nil {
		t.Skipf("native Bash unavailable: %v", err)
	}
	model := &backgroundTaskModel{}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return config.Config{APIKeys: map[string]string{"deepseek": "test-key"}}, nil
		},
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		userHomeDir: func() (string, error) { return t.TempDir(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{"--workspace", t.TempDir(), "--print", "run a background task"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if output.String() != "completed\n" || len(model.requests) != 4 {
		t.Fatalf("output = %q after %d requests", output.String(), len(model.requests))
	}
	if model.outputPath == "" {
		t.Fatal("bash did not report an output file")
	}

	messages := model.requests[2].Messages
	notice, ok := messages[len(messages)-1].(llm.UserMessage)
	if !ok || !notice.IsNotice() || !strings.Contains(notice.Content[0].Text, "not from the user") ||
		!strings.Contains(notice.Content[0].Text, "from-task") {
		t.Fatalf("message after the completion = %#v", messages[len(messages)-1])
	}
	waited, ok := messages[len(messages)-2].(llm.ToolResultMessage)
	if !ok || len(waited.Content) != 1 || !strings.Contains(waited.Content[0].Text, "exit code: 0") {
		t.Fatalf("foreground result before the notification = %#v", messages[len(messages)-2])
	}
	read := requestToolResult(t, model.requests[3])
	if read.IsError || !strings.Contains(read.Content[0].Text, "from-task") {
		t.Fatalf("reading the output file = %#v", read)
	}
	if _, err := os.Stat(filepath.Dir(model.outputPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("task directory after the run: %v", err)
	}
}

var outputFilePattern = regexp.MustCompile(`Output file: (\S+)`)

type backgroundTaskModel struct {
	requests   []llm.Request
	outputPath string
}

func (m *backgroundTaskModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.requests = append(m.requests, request)
	call := func(id, name string, arguments any) llm.Stream {
		data, _ := json.Marshal(arguments)
		return toolCallEventStream(request.Model, llm.ToolCall{ID: id, Name: name, Arguments: data})
	}
	switch len(m.requests) {
	case 1:
		return call("start", "bash", map[string]any{
			"command": "sleep 0.5; echo from-task", "description": "start", "run_in_background": true,
		}), nil
	case 2:
		if result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage); ok {
			if match := outputFilePattern.FindStringSubmatch(result.Content[0].Text); match != nil {
				m.outputPath = match[1]
			}
		}
		return call("wait", "bash", map[string]any{"command": "sleep 2", "description": "wait"}), nil
	case 3:
		return call("read", "read", map[string]any{"path": m.outputPath}), nil
	}
	return textEventStream(request.Model, "completed", llm.Usage{}, llm.StopReasonStop), nil
}

func requestToolResult(t *testing.T, request llm.Request) llm.ToolResultMessage {
	t.Helper()
	result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if !ok {
		t.Fatalf("last request message = %#v, want a tool result", request.Messages[len(request.Messages)-1])
	}
	return result
}

// Tasks belong to the Session that started them: /new stops them.
func TestInteractiveSessionNewStopsBackgroundTasks(t *testing.T) {
	t.Parallel()
	shell, err := deps.FindNativeBash(deps.DefaultOptions())
	if err != nil {
		t.Skipf("native Bash unavailable: %v", err)
	}
	workspace := t.TempDir()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	bash, err := tool.NewBash(t.Context(), testWorkspace(t, workspace), tool.BashShells{Native: shell}, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bash.Execute(t.Context(), llm.ToolCall{ID: "start", Name: "bash",
		Arguments: json.RawMessage(`{"command":"sleep 60","description":"wait","run_in_background":true}`)}); err != nil {
		t.Fatal(err)
	}
	store := createAppTestSession(t, filepath.Join(t.TempDir(), "session.jsonl"), workspace)
	t.Cleanup(func() { _ = store.Close() })
	runner := &interactiveSession{conversation: conversationState{store: store}, tasks: tasks}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new"}); err != nil {
		t.Fatal(err)
	}
	if running := tasks.Running(); running != 0 {
		t.Fatalf("running tasks after /new = %d", running)
	}
}

// A subagent /new stops is reported to the Session that started it, so its
// report stays in that history and its usage in that Session's totals.
func TestInteractiveSessionNewRecordsStoppedSubagents(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	if _, err := tasks.StartRun(tool.TaskAgent, "survey", nil, func(ctx context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		_, _ = io.WriteString(out, "partial survey")
		<-ctx.Done()
		return &llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{InputTokens: 300, OutputTokens: 40}}, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := createAppTestSession(t, path, workspace)
	runner := &interactiveSession{conversation: conversationState{store: store}, tasks: tasks}
	output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new"})
	if err != nil || strings.Contains(output, "warning") {
		t.Fatalf("/new = %q, %v", output, err)
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	summary, err := reopened.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Delegations != 1 || summary.DelegatedUsage.InputTokens != 300 || summary.Total.OutputTokens != 40 {
		t.Fatalf("previous Session usage = %+v, want the stopped subagent's", summary)
	}
}
