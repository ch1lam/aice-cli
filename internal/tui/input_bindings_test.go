package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

// A key must have one owner in the effective context, including disabled keys.
// This detects new collisions without depending on matcher iteration order.
func TestInputBindingsHaveOneOwnerPerContext(t *testing.T) {
	t.Parallel()
	for _, state := range []string{
		"main", "draft", "running", "clipboard", "delivery", "slash", "options", "empty options", "file", "pending file",
		"side", "side running", "side read-only", "side menu", "side confirm", "secret",
		"guard", "feedback", "auth waiting", "auth input", "auth menu",
		"sessions", "search", "preview", "rename", "saving", "restoring", "reading", "directory",
	} {
		t.Run(state, func(t *testing.T) {
			m := bindingTestModel(t, state)
			bindings := m.inputBindings()
			if len(bindings) == 0 {
				t.Fatal("context has no actions")
			}
			owners := make(map[string]inputAction)
			for _, binding := range bindings {
				for _, key := range binding.binding.Keys() {
					if previous, ok := owners[key]; ok {
						t.Fatalf("%q owned by both %s and %s", key, previous, binding.action)
					}
					owners[key] = binding.action
				}
			}
			// Rendering must remain a read-only projection of interaction state.
			before := m.inputIdentity()
			for _, width := range []int{12, 20, 40, 80, 240} {
				if help := m.inputHelp(width, false); ansi.StringWidth(help) > width {
					t.Fatalf("help overflows %d: %q", width, help)
				}
			}
			if m.inputIdentity() != before {
				t.Fatal("help mutated focus")
			}
		})
	}
}

func bindingTestModel(t *testing.T, state string) model {
	t.Helper()
	m := newModel(nil, nil)
	switch state {
	case "draft":
		m.input.SetValue("question?")
	case "running":
		m.running, m.acceptsDelivery = true, true
	case "clipboard":
		m.clipboardPending = true
	case "delivery":
		m.deliveryPending = true
	case "slash":
		m.input.SetValue("/he")
	case "options", "empty options":
		m = reasoningCompletionModel()
		m.input.SetValue("/thinking ")
		if state == "empty options" {
			m.input.SetValue("/thinking nonexistent")
		}
		m.syncCommandCompletion()
	case "file", "pending file":
		m = completionTestModel()
		m.input.SetValue("@file")
		m.requestFileCompletion()
		if state == "file" {
			m.fileCompletion.pending = false
			m.fileCompletion.items = []interaction.FileCompletion{{Path: "file.go"}}
		}
	case "side", "side running", "side read-only", "side menu", "side confirm":
		m.side.isVisible, m.side.activeID = true, 1
		thread := &sideThreadState{id: 1}
		m.side.threads[1] = thread
		switch state {
		case "side running":
			thread.isRunning = true
		case "side read-only":
			thread.status = interaction.SideThreadReadOnly
		case "side menu":
			m.side.menu = &sideMenuState{}
		case "side confirm":
			m.side.confirm = &sideConfirmState{threadID: 1}
		}
	case "secret":
		m.secretInput = &secretInput{}
	case "guard", "feedback":
		m.guardPending = &interaction.GuardRequest{Options: []interaction.GuardOption{{ID: "allow"}, {ID: "deny", Deny: true}}}
		m.guardFeedback = state == "feedback"
	case "auth waiting", "auth input", "auth menu":
		m.authInput = make(chan string, 1)
		m.authPrompt = &interaction.AuthPrompt{AllowInput: state == "auth input"}
		if state == "auth menu" {
			m.authPrompt.Menu = &interaction.CommandMenu{Options: []interaction.CommandOption{{Label: "one"}}}
		}
	case "sessions", "search", "preview", "rename", "saving", "restoring":
		m = pickerModel(t, 160, 30)
		switch state {
		case "search":
			m.sessionPicker.input.Focus()
		case "preview":
			m.sessionPicker.previewVisible, m.sessionPicker.previewFocused = true, true
		case "rename", "saving":
			m.renameSession = func(uint64, string, string) (tea.Cmd, context.CancelFunc) { return nil, func() {} }
			m.openSessionTitleEditor()
			m.sessionPicker.rename.saving = state == "saving"
		case "restoring":
			m.sessionPicker.restoring = true
		}
	case "reading", "directory":
		previous := m
		m.reading = &sessionReading{previous: &previous, directory: state == "directory"}
	}
	return m
}

func TestCompletionHelpAndDispatchReplaceSendTogether(t *testing.T) {
	t.Parallel()
	m := bindingTestModel(t, "file")
	if help := m.inputHelp(1000, true); !strings.Contains(help, "Tab/Enter attach") || strings.Contains(help, "Enter send") {
		t.Fatalf("completion help: %s", help)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.running || len(m.input.files) != 1 {
		t.Fatal("completion Enter sent instead of attaching")
	}
	if help := m.inputHelp(1000, true); strings.Contains(help, "attach") || !strings.Contains(help, "Enter send") {
		t.Fatalf("composer help after attaching: %s", help)
	}
	m = bindingTestModel(t, "pending file")
	before := m.input.Value()
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != before || m.running {
		t.Fatal("pending completion Enter escaped to send")
	}
	if help := m.inputHelp(1000, true); strings.Contains(help, "attach") || strings.Contains(help, "Enter send") {
		t.Fatalf("pending completion advertises confirm: %s", help)
	}
}

func TestPickerFocusRoundTripRejectsLatePaste(t *testing.T) {
	t.Parallel()
	m := bindingTestModel(t, "search")
	reply := m.scopeInputCommand(func() tea.Msg { return tea.PasteMsg{Content: "late search"} })
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.inputContext().focus != inputFocusPreview {
		t.Fatal("preview focus not projected")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.inputContext().focus != inputFocusSearch {
		t.Fatal("search focus not restored")
	}
	before := m.sessionPicker.input.Value()
	m = updateModel(t, m, reply())
	if m.sessionPicker.input.Value() != before {
		t.Fatal("late paste crossed focus round trip")
	}
}

func TestNarrowHelpKeepsConfirmationAndCancellation(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"guard", "feedback", "options", "file", "side confirm", "secret"} {
		t.Run(state, func(t *testing.T) {
			m := bindingTestModel(t, state)
			help := m.inputHelp(20, false)
			if !strings.Contains(help, "Enter") || !strings.Contains(help, "Esc") || ansi.StringWidth(help) > 20 {
				t.Fatalf("primary controls lost: %q", help)
			}
		})
	}
}

func TestProcessHeadingOnlyAdvertisesActiveBinding(t *testing.T) {
	t.Parallel()
	m := newModel(nil, nil)
	m.keys.process.SetKeys("f6")
	m.keys.process.SetHelp("F6", "process")
	if heading := m.processHeader(0, 0, true); !strings.Contains(heading, "F6 to expand") || strings.Contains(heading, "Ctrl+o") {
		t.Fatalf("heading ignored action definition: %s", heading)
	}
	previous := m
	m.reading = &sessionReading{previous: &previous}
	if heading := m.processHeader(0, 0, true); strings.Contains(heading, "F6") || strings.Contains(heading, "Ctrl+o") {
		t.Fatalf("reader advertised background action: %s", heading)
	}
}

func TestExpandedHelpKeepsAvailableActionsAtNarrowWidths(t *testing.T) {
	t.Parallel()
	for _, width := range []int{40, 80, 160} {
		m := newModel(nil, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 50})
		m.help.ShowAll = true
		m.clipboard = func() tea.Msg { return nil }
		m.searchSessions = func(uint64, string) (tea.Cmd, context.CancelFunc) { return nil, func() {} }
		view := ansi.Strip(m.footerView(width))
		for _, binding := range m.footerKeys().help(true) {
			if !strings.Contains(view, binding.Help().Key) {
				t.Fatalf("width %d lost %q from expanded help: %s", width, binding.Help().Key, view)
			}
		}
	}
}
