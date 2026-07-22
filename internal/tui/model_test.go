package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestModelSubmitsPromptAndConsumesAgentEvents(t *testing.T) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	controllerDone := make(chan struct{})
	current := newModel(requests, controllerDone)
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("inspect this repository")

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !handled || command == nil {
		t.Fatal("enter did not submit the prompt")
	}
	if !updated.running || updated.input.Focused() {
		t.Fatal("model did not enter running state and blur input")
	}

	rawStartMessage := command()
	startMessage, ok := rawStartMessage.(runStartedMsg)
	if !ok {
		t.Fatalf("start command message = %T, want runStartedMsg", rawStartMessage)
	}
	request := <-requests
	if request.prompt != "inspect this repository" {
		t.Errorf("run prompt = %q, want inspect this repository", request.prompt)
	}
	updated = updateModel(t, updated, startMessage)

	identity := llm.Model{ID: "test", API: "test", Provider: "test"}
	startedAssistant := llm.NewAssistantMessage(identity)
	completedAssistant := llm.NewAssistantMessage(identity)
	completedAssistant.Content = []llm.ContentPart{
		llm.NewThinkingContent("checking context", "").Part(),
		llm.NewTextContent("**Inspection complete.**").Part(),
	}
	completedAssistant.StopReason = llm.StopReasonStop
	call := llm.ToolCall{ID: "call-1", Name: "read", Arguments: []byte(`{"path":"go.mod"}`)}

	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{
		{cancel: func() {}},
		{event: agent.Event{
			Type:             agent.EventTypeMessageStart,
			AssistantMessage: &startedAssistant,
		}},
		{event: agent.Event{
			Type: agent.EventTypeMessageUpdate,
			StreamEvent: &llm.Event{
				Type:  llm.EventTypeTextDelta,
				Delta: "Inspection",
			},
		}},
		{event: agent.Event{
			Type:     agent.EventTypeToolExecutionStart,
			ToolCall: &call,
		}},
		{event: agent.Event{
			Type:     agent.EventTypeToolExecutionEnd,
			ToolCall: &call,
		}},
		{event: agent.Event{
			Type:             agent.EventTypeMessageEnd,
			AssistantMessage: &completedAssistant,
		}},
		{done: true},
	}})

	if updated.running {
		t.Fatal("model remains running after terminal update")
	}
	if !updated.input.Focused() {
		t.Fatal("input is not focused after terminal update")
	}
	if updated.cancelRun != nil {
		t.Fatal("run cancellation function remains after completion")
	}
	transcript := updated.transcriptView()
	for _, want := range []string{
		"inspect this repository",
		"checking context",
		"Inspection complete.",
		"read",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript does not contain %q: %q", want, transcript)
		}
	}
}

func TestModelControlCCancelsOnlyActiveRun(t *testing.T) {
	t.Parallel()

	cancelled := false
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.cancelRun = func() { cancelled = true }

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'c',
		Mod:  tea.ModCtrl,
	}))
	if !handled || command != nil {
		t.Fatal("ctrl+c should cancel an active run without quitting")
	}
	if !cancelled {
		t.Fatal("ctrl+c did not invoke active run cancellation")
	}
	if updated.status != "Cancelling current response..." {
		t.Errorf("status = %q, want cancellation status", updated.status)
	}
}

func TestModelControlCBeforeRunStartsDefersCancellation(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'c',
		Mod:  tea.ModCtrl,
	}))
	if !handled || command != nil {
		t.Fatal("ctrl+c should defer cancellation while a run is starting")
	}
	if !updated.cancelRequested {
		t.Fatal("model did not remember cancellation requested before run start")
	}

	cancelled := false
	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{
		{cancel: func() { cancelled = true }},
	}})
	if !cancelled {
		t.Fatal("deferred cancellation did not cancel the started run")
	}
}

func TestModelHelpTogglesAndUsesAvailableHeight(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	collapsedHeight := current.viewport.Height()

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyF1}))
	if !handled || command != nil {
		t.Fatal("f1 did not toggle help")
	}
	if !updated.help.ShowAll {
		t.Fatal("help remains collapsed after f1")
	}
	if updated.viewport.Height() >= collapsedHeight {
		t.Errorf(
			"expanded help viewport height = %d, want less than %d",
			updated.viewport.Height(),
			collapsedHeight,
		)
	}
	if !updated.viewport.AtBottom() {
		t.Fatal("expanded help left the empty welcome viewport scrollable")
	}
}

func TestModelCancellationIsRenderedAsNotice(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.finishRun(context.Canceled)

	transcript := current.transcriptView()
	if !strings.Contains(transcript, "Response cancelled") {
		t.Fatalf("transcript does not contain cancellation notice: %q", transcript)
	}
	if strings.Contains(transcript, "Error") {
		t.Fatalf("cancellation was rendered as an error: %q", transcript)
	}
}

func updateModel(t *testing.T, current model, message tea.Msg) model {
	t.Helper()
	updated, _ := current.Update(message)
	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updated)
	}
	return result
}
