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
	toolResult := llm.ToolResultMessage{
		Role:       llm.RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    []llm.ContentPart{llm.NewTextContent("module contents").Part()},
	}

	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{
		{cancel: func() {}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageStart,
			Message: startedAssistant,
		}},
		{event: agent.AgentEvent{
			Type: agent.EventTypeMessageUpdate,
			AssistantMessageEvent: &llm.Event{
				Type:  llm.EventTypeTextDelta,
				Delta: "Inspection",
			},
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageEnd,
			Message: completedAssistant,
		}},
		{event: agent.AgentEvent{
			Type:     agent.EventTypeToolExecutionStart,
			ToolCall: &call,
		}},
		{event: agent.AgentEvent{
			Type:     agent.EventTypeToolExecutionEnd,
			ToolCall: &call,
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageStart,
			Message: toolResult,
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageEnd,
			Message: toolResult,
		}},
		{event: agent.AgentEvent{
			Type: agent.EventTypeAgentEnd,
			Messages: []llm.AgentMessage{
				completedAssistant,
				toolResult,
			},
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
	if len(updated.entries) != 3 {
		t.Fatalf("transcript entries = %#v, want user, assistant, and tool", updated.entries)
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

func TestModelFocusedInputKeysDoNotScrollTranscript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       tea.KeyPressMsg
		wantInput string
	}{
		{name: "j", key: tea.KeyPressMsg{Code: 'j', Text: "j"}, wantInput: "prefixj"},
		{name: "k", key: tea.KeyPressMsg{Code: 'k', Text: "k"}, wantInput: "prefixk"},
		{name: "f", key: tea.KeyPressMsg{Code: 'f', Text: "f"}, wantInput: "prefixf"},
		{name: "b", key: tea.KeyPressMsg{Code: 'b', Text: "b"}, wantInput: "prefixb"},
		{name: "u", key: tea.KeyPressMsg{Code: 'u', Text: "u"}, wantInput: "prefixu"},
		{name: "d", key: tea.KeyPressMsg{Code: 'd', Text: "d"}, wantInput: "prefixd"},
		{name: "space", key: tea.KeyPressMsg{Code: ' ', Text: " "}, wantInput: "prefix "},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}, wantInput: "prefix"},
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}, wantInput: "prefix"},
		{
			name:      "control u",
			key:       tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl},
			wantInput: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newScrollableModel(t)
			current.input.SetValue("prefix")
			initialOffset := current.viewport.YOffset()

			updated := updateModel(t, current, tt.key)

			if got := updated.viewport.YOffset(); got != initialOffset {
				t.Errorf("viewport Y offset = %d, want %d", got, initialOffset)
			}
			if got := updated.input.Value(); got != tt.wantInput {
				t.Errorf("input value = %q, want %q", got, tt.wantInput)
			}
		})
	}
}

func TestModelViewportAcceptsOnlyPublishedKeyboardScrollKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        tea.KeyPressMsg
		wantScroll bool
	}{
		{name: "j", key: tea.KeyPressMsg{Code: 'j', Text: "j"}},
		{name: "k", key: tea.KeyPressMsg{Code: 'k', Text: "k"}},
		{name: "f", key: tea.KeyPressMsg{Code: 'f', Text: "f"}},
		{name: "b", key: tea.KeyPressMsg{Code: 'b', Text: "b"}},
		{name: "u", key: tea.KeyPressMsg{Code: 'u', Text: "u"}},
		{name: "d", key: tea.KeyPressMsg{Code: 'd', Text: "d"}},
		{name: "space", key: tea.KeyPressMsg{Code: ' ', Text: " "}},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}},
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}},
		{name: "page up", key: tea.KeyPressMsg{Code: tea.KeyPgUp}, wantScroll: true},
		{name: "page down", key: tea.KeyPressMsg{Code: tea.KeyPgDown}, wantScroll: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newScrollableModel(t)
			current.running = true
			current.input.Blur()
			initialOffset := current.viewport.YOffset()

			updated := updateModel(t, current, tt.key)

			scrolled := updated.viewport.YOffset() != initialOffset
			if scrolled != tt.wantScroll {
				t.Errorf(
					"viewport Y offset = %d, initial %d, want scroll %v",
					updated.viewport.YOffset(),
					initialOffset,
					tt.wantScroll,
				)
			}
		})
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

func TestModelPlacesComposerAboveStatusAndHelp(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("composer marker")
	current.input.Blur()

	content := current.View().Content
	composerIndex := strings.Index(content, "composer marker")
	statusIndex := strings.Index(content, "● "+current.status)
	helpIndex := strings.Index(content, "enter")
	if composerIndex < 0 || statusIndex < 0 || helpIndex < 0 {
		t.Fatalf(
			"view is missing composer, status, or help: composer=%d status=%d help=%d",
			composerIndex,
			statusIndex,
			helpIndex,
		)
	}
	if composerIndex >= statusIndex || composerIndex >= helpIndex {
		t.Fatalf(
			"composer is not above status and help: composer=%d status=%d help=%d",
			composerIndex,
			statusIndex,
			helpIndex,
		)
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

func newScrollableModel(t *testing.T) model {
	t.Helper()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.viewport.SetContent(strings.Repeat("line\n", 100))
	current.viewport.SetYOffset(20)
	return current
}
