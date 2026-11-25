package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
)

// A frontend that follows subagents sees every child run as its start, the
// child's own events and its end, under the child's ID: the call that runs a
// synchronous child, the task of a background one; its start names the call.
func TestInteractiveSubagentRunsReachTheFrontend(t *testing.T) {
	t.Parallel()
	for _, background := range []bool{false, true} {
		name := map[bool]string{false: "synchronous", true: "background"}[background]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := &followedChildModel{background: background}
			var mu sync.Mutex
			var updates []interaction.AgentUpdate
			ended := make(chan struct{})
			command, err := newTestCommand(t, dependencies{
				loadConfig: func(config.LoadOptions) (config.Config, error) {
					return config.Config{APIKeys: map[string]string{"deepseek": "test-key"}}, nil
				},
				newModel:    func(config.Config) (agent.Model, error) { return model, nil },
				userHomeDir: func() (string, error) { return t.TempDir(), nil },
				runTUI: func(ctx context.Context, frontend interaction.Frontend, _ tui.Options) error {
					if frontend.Agents == nil {
						t.Fatal("the TUI frontend does not follow subagents")
					}
					go func() {
						for {
							select {
							case update := <-frontend.Agents.AgentUpdates():
								mu.Lock()
								updates = append(updates, update)
								mu.Unlock()
								if update.End != nil {
									close(ended)
								}
							case <-ctx.Done():
								return
							}
						}
					}()
					if err := runInteractive(ctx, frontend.Runner, "investigate", nil); err != nil {
						return err
					}
					select {
					case <-ended:
					case <-time.After(10 * time.Second):
						t.Error("the child's end never arrived")
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			command.SetIn(strings.NewReader(""))
			command.SetOut(io.Discard)
			command.SetArgs([]string{"--workspace", t.TempDir()})
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if err := command.ExecuteContext(ctx); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(updates) < 2 {
				t.Fatalf("updates = %#v", updates)
			}
			start, end := updates[0], updates[len(updates)-1]
			id := start.ID
			if background {
				model.mu.Lock()
				id = model.task
				model.mu.Unlock()
			} else if !regexp.MustCompile(`^agent-[0-9a-f]{6}$`).MatchString(id) {
				t.Fatalf("synchronous child ID = %q, want its own", id)
			}
			want := interaction.AgentStart{ToolCallID: "delegate", Description: "find loader", Type: "explore",
				Background: background, Model: start.Start.Model, Prompt: "find the config loader"}
			if start.ID != id || start.Start == nil || *start.Start != want || start.Start.Model.ID == "" {
				t.Fatalf("first update = %#v, want the start of %s", start, id)
			}
			if end.ID != id || end.End == nil || end.End.Err != nil || end.End.Turns != 2 || end.End.ToolCalls != 1 || end.End.Duration <= 0 {
				t.Fatalf("last update = %#v, want the end of %s", end, id)
			}
			var kinds []interaction.EventKind
			for _, update := range updates[1 : len(updates)-1] {
				if update.ID != id || update.Event == nil {
					t.Fatalf("update between start and end = %#v", update)
				}
				switch update.Event.Kind {
				case interaction.EventToolStart, interaction.EventToolEnd, interaction.EventAssistantEnd:
					kinds = append(kinds, update.Event.Kind)
					if update.Event.Kind == interaction.EventToolStart && (update.Event.Tool.Name != "ls" || update.Event.Tool.Kind != interaction.ToolKindRead) {
						t.Fatalf("tool start = %#v, want ls described", update.Event.Tool)
					}
				}
			}
			wantKinds := []interaction.EventKind{interaction.EventAssistantEnd, interaction.EventToolStart, interaction.EventToolEnd, interaction.EventAssistantEnd}
			if !slices.Equal(kinds, wantKinds) {
				t.Fatalf("child events = %v, want %v", kinds, wantKinds)
			}
		})
	}
}

// followedChildModel delegates one explore task, synchronously or in the
// background, whose child lists the workspace once before answering.
type followedChildModel struct {
	background bool
	mu         sync.Mutex
	parent     int
	task       string
}

func (m *followedChildModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(request.Tools) == 0 {
		return textEventStream(request.Model, "Title", llm.Usage{}, llm.StopReasonStop), nil
	}
	call := func(id, name string, arguments any) llm.Stream {
		data, _ := json.Marshal(arguments)
		return toolCallEventStream(request.Model, llm.ToolCall{ID: id, Name: name, Arguments: data})
	}
	if strings.Contains(request.SystemPrompt, "# Subagent") {
		if len(request.Messages) == 1 {
			return call("list", "ls", map[string]any{"path": "."}), nil
		}
		return textEventStream(request.Model, "the loader is config.Load", llm.Usage{}, llm.StopReasonStop), nil
	}
	m.parent++
	switch m.parent {
	case 1:
		return call("delegate", "agent", map[string]any{"description": "find loader", "prompt": "find the config loader",
			"subagent_type": "explore", "run_in_background": m.background}), nil
	case 2:
		if m.background {
			if started, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage); ok {
				m.task = regexp.MustCompile(`bg-[0-9a-f]{6}`).FindString(started.Content[0].Text)
			}
			return call("wait", "task_output", map[string]any{"task_ids": []string{m.task}, "wait": 20}), nil
		}
	}
	return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop), nil
}

// A child never waits for a frontend that stopped receiving, nor past its
// own cancellation, even with the buffer full.
func TestPublishAgentStopsWaiting(t *testing.T) {
	t.Parallel()
	s := &interactiveSession{}
	stop := s.followAgents()
	for range agentUpdateBuffer {
		s.publishAgent(t.Context(), interaction.AgentUpdate{ID: "fill"})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancelled := make(chan struct{})
	go func() {
		s.publishAgent(ctx, interaction.AgentUpdate{ID: "cancelled"})
		close(cancelled)
	}()
	cancel()
	<-cancelled

	stopped := make(chan struct{})
	go func() {
		s.publishAgent(context.WithoutCancel(t.Context()), interaction.AgentUpdate{ID: "after stop"})
		close(stopped)
	}()
	stop()
	stop()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("publishAgent still waits after the frontend stopped")
	}
}

// Without a frontend that follows subagents, a child run has no event sink.
func TestFollowChildWithoutFrontendIsSilent(t *testing.T) {
	t.Parallel()
	d := &delegation{}
	sink, settle := d.followChild(t.Context(), tool.DelegateRequest{}, childRun{id: "x"}, configuredModel{}, nil, "task")
	if sink != nil {
		t.Fatal("a child without a follower got an event sink")
	}
	settle(tool.DelegateResult{})
}

// A person stops background children only; a synchronous child stops with
// the response that waits for it, and an unknown task is refused.
func TestStopAgentStopsBackgroundChildren(t *testing.T) {
	t.Parallel()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	s := &interactiveSession{tasks: tasks}
	status, err := tasks.StartRun(tool.TaskAgent, "wait", nil, func(ctx context.Context, _ string, _ io.Writer) (*llm.ToolDelegation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StopAgent(syncAgentID()); !errors.Is(err, errStopSynchronous) {
		t.Fatalf("synchronous stop err = %v", err)
	}
	if err := s.StopAgent("bg-missing"); err == nil {
		t.Fatal("an unknown task stopped")
	}
	done, err := tasks.Done(status.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StopAgent(status.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the background child did not stop")
	}
}
