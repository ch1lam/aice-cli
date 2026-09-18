package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionRenameEditorSavesWithoutSwitchingAndPreservesSelection(t *testing.T) {
	m := pickerModel(t, 100, 28)
	m.sessionPicker.list.Select(2)
	m.renameSession = func(generation uint64, key, title string) (tea.Cmd, context.CancelFunc) {
		if key != "two" || title != "新名字" {
			t.Fatal(key, title)
		}
		return func() tea.Msg {
			return sessionRenameResult{generation: generation, item: interaction.SessionSummary{Key: key, ID: key, Title: title, UpdatedAt: 300}}
		}, func() {}
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.sessionPicker.rename == nil || !strings.Contains(ansi.Strip(m.View().Content), "RENAME SESSION") || m.View().Cursor == nil {
		t.Fatal("rename editor not visible")
	}
	m.sessionPicker.rename.input.SetValue("新名字")
	next, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.running || !m.sessionPicker.rename.saving || m.View().Cursor != nil {
		t.Fatal("rename dispatched an agent run or exposed save cursor")
	}
	m = updateModel(t, m, command())
	if m.sessionPicker.rename != nil || selectedSessionKey(m.sessionPicker) != "two" || m.sessionPicker.list.SelectedItem().(sessionListItem).Title != "新名字" || m.input.Value() != "keep my draft" {
		t.Fatal("save lost selection, title or draft")
	}
}

func TestSessionRenameFailureCancelAndStaleResults(t *testing.T) {
	m := pickerModel(t, 55, 18)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	generation := m.sessionQueryGeneration
	m.sessionPicker.rename.saving = true
	m = updateModel(t, m, sessionRenameResult{generation: generation, err: errors.New("busy writer")})
	if m.sessionPicker.rename == nil || m.sessionPicker.rename.saving || !strings.Contains(m.sessionPicker.notice, "busy writer") {
		t.Fatal("failed save lost editable title")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.sessionPicker == nil || m.sessionPicker.rename != nil || m.input.Value() != "keep my draft" {
		t.Fatal("cancel lost picker or draft")
	}
	m = updateModel(t, m, sessionRenameResult{generation: generation, item: interaction.SessionSummary{Key: "one", Title: "stale"}})
	if strings.Contains(m.sessionPickerView(), "stale") {
		t.Fatal("late save altered cancelled editor")
	}
}
