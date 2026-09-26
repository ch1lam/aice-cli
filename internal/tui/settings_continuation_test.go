package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

func continuationResult(m *model) settingsActionDone {
	a := &settingsAction{}
	m.settings.action = a
	return settingsActionDone{action: a, result: interaction.SettingsActionResult{
		Committed: true, Applied: true, Output: "Computer Use enabled",
		Continuation: &interaction.TaskContinuation{SessionID: "session", LeafID: "leaf", Revision: 4, Prompt: "Continue from recorded progress"},
	}}
}

func TestSettingsContinuationRequiresExplicitChoiceAndPreservesDraft(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"keyboard", "mouse"} {
		t.Run(mode, func(t *testing.T) {
			m := panelModel(t, 80, 24)
			requests := make(chan runRequest, 1)
			m.requests = requests
			m.insertImagePlaceholder(llm.ImageContent{Data: []byte("synthetic")})
			m.input.files = []composerFile{{path: "draft.txt"}}
			draft := composerDraft{text: m.input.Value(), pastes: m.pastes, files: m.input.files}
			m.pendingDeliveries = []pendingDelivery{{id: "old", text: "old follow-up", mode: deliveryQueue}}
			done := continuationResult(&m)
			m = updateModel(t, m, done)
			if m.running || len(m.entries) != 0 || len(m.promptHistory) != 0 || len(requests) != 0 {
				t.Fatal("setup automatically submitted")
			}
			if !strings.Contains(ansi.Strip(m.settingsPanelView()), "[Continue] F6") {
				t.Fatal("missing explicit continuation")
			}
			var next tea.Model
			var cmd tea.Cmd
			if mode == "mouse" {
				l := m.settings.layout
				mouse := tea.Mouse{X: l.x + 3, Y: l.y + l.height - 2, Button: tea.MouseLeft}
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				next, cmd = m.settingsPointer(tea.MouseReleaseMsg(mouse))
			} else {
				next, cmd = m.handleSettings(tea.KeyPressMsg{Code: tea.KeyF6})
			}
			m = next.(model)
			if cmd == nil || !m.running || m.settings != nil || len(m.pendingDeliveries) != 0 {
				t.Fatal("continuation did not start a fresh run")
			}
			_ = cmd()
			request := <-requests
			if request.continuation == nil || request.prompt != done.result.Continuation.Prompt || len(request.files) != 0 || len(request.images) != 0 {
				t.Fatal("request included unrelated input")
			}
			if m.input.Value() != draft.text || !reflect.DeepEqual(m.pastes, draft.pastes) || !reflect.DeepEqual(m.input.files, draft.files) {
				t.Fatal("draft changed")
			}
			// A rejected preparation must not replace even subsequent composer edits.
			m.input.InsertString("new edit")
			text := m.input.Value()
			m.restoreSubmittedInput()
			if m.input.Value() != text || len(m.entries) != 0 {
				t.Fatal("rejection rewrote composer or retained unaccepted prompt")
			}
		})
	}
}

func TestSettingsContinuationIsNotOfferedForIncompleteOrStaleResult(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"cancel", "error", "uncommitted", "unapplied", "old-panel", "escape"} {
		t.Run(kind, func(t *testing.T) {
			m := panelModel(t, 80, 24)
			done := continuationResult(&m)
			switch kind {
			case "cancel":
				done.action.cancelled = true
			case "error":
				done.err = errors.New("setup incomplete")
			case "uncommitted":
				done.result.Committed = false
			case "unapplied":
				done.result.Applied = false
			case "old-panel":
				m.settings.action = &settingsAction{}
			}
			m = updateModel(t, m, done)
			if kind == "escape" {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			if m.canContinueTask() {
				t.Fatal("stale continuation available")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
			if m.running || len(m.entries) != 0 {
				t.Fatal("stale proposal ran")
			}
		})
	}
}
