package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionHelpFollowsFocusAndKeepsNoticesSeparate(t *testing.T) {
	m := pickerModel(t, 200, 30)
	if help := m.inputHelp(500, true); !strings.Contains(help, "↑↓ select") || !strings.Contains(help, "/ search") {
		t.Fatalf("list help = %q", help)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m.sessionPicker.notice = "Loading selected preview…"
	help := m.inputHelp(500, true)
	if !strings.Contains(help, "↑↓ scroll") || strings.Contains(help, "↑↓ select") || !strings.Contains(help, "Esc hide preview") {
		t.Fatalf("preview help = %q", help)
	}
	view := ansi.Strip(m.sessionPickerView())
	if !strings.Contains(view, "Loading selected preview…") || !strings.Contains(view, "Esc hide preview") {
		t.Fatal("notice replaced available actions")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if strings.Contains(m.inputHelp(500, true), "/ search") {
		t.Fatal("focused search advertises slash as a shortcut")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.sessionPicker.input.Value() != "/" {
		t.Fatal("slash shortcut intercepted search text")
	}
}

func TestSessionUnavailableActionsAreHiddenAndCannotReachComposer(t *testing.T) {
	m := pickerModel(t, 160, 30)
	m.setSessionItems([]interaction.SessionSummary{{Key: "bad", Title: "Unavailable", Problem: "Broken session"}})
	help := m.inputHelp(500, true)
	for _, label := range []string{"resume", "rename", "read"} {
		if strings.Contains(help, label) {
			t.Fatalf("unavailable action in help: %q", help)
		}
	}
	for _, code := range []rune{tea.KeyEnter, tea.KeyF2, tea.KeyF4} {
		m = updateModel(t, m, tea.KeyPressMsg{Code: code})
	}
	if m.sessionPicker.rename != nil || m.running || m.input.Value() != "keep my draft" || m.sessionPicker.notice != "Broken session" {
		t.Fatal("disabled session action executed or leaked input")
	}
}

func TestSessionTitleSavePendingAcceptsOnlyCancel(t *testing.T) {
	m := pickerModel(t, 160, 30)
	saves := 0
	m.renameSession = func(uint64, string, string) (tea.Cmd, context.CancelFunc) {
		saves++
		return nil, func() {}
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	before := m.sessionPicker.rename.input.Value()
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: tea.KeyF2},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.PasteMsg{Content: "changed"},
	} {
		m = updateModel(t, m, msg)
	}
	if saves != 1 || m.sessionPicker.rename.input.Value() != before || strings.Contains(m.inputHelp(500, true), "save") {
		t.Fatal("pending save accepted input or advertised another save")
	}
	if !strings.Contains(ansi.Strip(m.sessionPickerView()), "Esc cancel") {
		t.Fatal("save progress hides cancellation")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModCtrl})
	if m.sessionPicker.rename == nil {
		t.Fatal("modified Escape matched the unmodified cancel shortcut")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.sessionPicker == nil || m.sessionPicker.rename != nil {
		t.Fatal("cancel did not return to picker")
	}
}
