package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestInputDomainsOwnPasteAndKeyRelease(t *testing.T) {
	for _, domain := range []inputDomain{inputReading, inputGuard, inputSideMenu, inputSideConfirm} {
		t.Run(string(rune('A'+domain)), func(t *testing.T) {
			m := newModel(nil, nil)
			m.input.SetValue("saved draft")
			switch domain {
			case inputReading:
				m.reading = &sessionReading{}
			case inputGuard:
				m.guardPending = &interaction.GuardRequest{}
			case inputSideMenu:
				m.side.menu = &sideMenuState{}
			case inputSideConfirm:
				m.side.confirm = &sideConfirmState{}
			}
			before := m.inputContext()
			if before != m.inputContext() || before.domain != domain || before.editor {
				t.Fatal("input context is not a stable exclusive projection")
			}
			m = updateModel(t, m, tea.PasteMsg{Content: "must not leak"})
			m = updateModel(t, m, tea.KeyReleaseMsg{Code: 'x', Text: "x"})
			if m.input.Value() != "saved draft" || m.input.Focused() {
				t.Fatal("exclusive domain leaked input or composer focus")
			}
		})
	}
}

func TestInputComponentRepliesStayWithTheirEditor(t *testing.T) {
	for _, boundary := range []string{"current", "clear", "guard round trip", "picker rename", "auth step"} {
		t.Run(boundary, func(t *testing.T) {
			m := newModel(nil, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			if boundary == "picker rename" {
				m = pickerModel(t, 100, 28)
			}
			if boundary == "auth step" {
				m.running = true
				m.authInput = make(chan string, 1)
				m.authPrompt = &interaction.AuthPrompt{AllowInput: true}
			}
			// A fake component command avoids reading the host clipboard. It
			// follows the same scoped result path as Bubbles' private paste reply.
			command := m.scopeInputCommand(func() tea.Msg { return tea.PasteMsg{Content: "late"} })
			switch boundary {
			case "clear":
				m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			case "guard round trip":
				req := &interaction.GuardRequest{Reply: make(chan interaction.GuardReply, 1)}
				m = updateModel(t, m, guardRequestMsg{req: req})
				m = updateModel(t, m, guardExpiredMsg{reply: req.Reply})
			case "auth step":
				m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{auth: &interaction.AuthPrompt{AllowInput: true, Title: "Next step"}}}})
			case "picker rename":
				// Both editors use textinput, so type-based routing alone would
				// let the search clipboard result enter the rename field.
				m.renameSession = func(uint64, string, string) (tea.Cmd, context.CancelFunc) { return nil, nil }
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
			}
			before := m.input.Value()
			var title string
			if m.sessionPicker != nil && m.sessionPicker.rename != nil {
				title = m.sessionPicker.rename.input.Value()
			}
			m = updateModel(t, m, command())
			if boundary == "current" {
				if m.input.Value() != "late" {
					t.Fatal("current editor result was dropped or applied twice")
				}
			} else if m.input.Value() != before {
				t.Fatal("stale editor reply crossed an input boundary")
			}
			if title != "" && m.sessionPicker.rename.input.Value() != title {
				t.Fatal("search paste entered rename input")
			}
		})
	}
}

func TestClearQuitSequenceIgnoresBackgroundButResetsOnInteraction(t *testing.T) {
	for _, event := range []tea.Msg{struct{}{}, copyNoticeExpiredMsg(99), tea.MouseWheelMsg{Button: tea.MouseWheelDown}, tea.BlurMsg{}} {
		m := newModel(nil, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		m = updateModel(t, m, event)
		want := true
		switch event.(type) {
		case tea.MouseWheelMsg, tea.BlurMsg:
			want = false
		}
		if m.clearQuitPending != want {
			t.Fatalf("event %T: clear/quit pending = %v", event, m.clearQuitPending)
		}
	}
}

func TestUnknownMessagesDoNotRebuildCompletion(t *testing.T) {
	m := newModel(nil, nil)
	m.input.SetValue("@file")
	m.fileCompletion.generation = 42
	m = updateModel(t, m, struct{}{})
	if m.fileCompletion.generation != 42 {
		t.Fatal("unrelated system message rebuilt completion")
	}
}
