package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestDesktopDeepLinkUsesSettingsWithoutStartingRun(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 80, 24)
	m.closeSettings()
	m.running = true
	m.input.SetValue("/desktop")
	if !m.isInfoCommandInput() {
		t.Fatal("deep-link would become steering input")
	}
	next, cmd, _ := m.submit()
	if cmd == nil || next.settings == nil || next.settings.focusField == "" {
		t.Fatal("deep-link did not open settings")
	}
	read := cmd().(settingsReadResult)
	read.snapshot.Fields = append(read.snapshot.Fields, interaction.SettingField{ID: "desktop_enabled", Category: "tools", Label: "Computer Use", Kind: interaction.SettingBool})
	m = updateModel(t, next, read)
	if m.settings.fields()[m.settings.selection].ID != "desktop_enabled" || m.settings.tab != 1 || len(m.entries) != 0 || len(m.promptHistory) != 0 || !m.running {
		t.Fatal("deep-link changed run/history or selected the wrong setting")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.settings != nil || m.cancelRequested {
		t.Fatal("closing settings cancelled run")
	}
}

func TestSettingsStopUsesExistingCancellationWithoutPatch(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"keyboard", "mouse", "preparing"} {
		t.Run(mode, func(t *testing.T) {
			m := panelModel(t, 80, 24)
			m.running = true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode != "preparing" {
				m.cancelRun = cancel
			}
			draft, revision := m.input.Value(), m.settings.snapshot.Revision
			m.writeSettings = func(uint64, interaction.SettingsRequest) tea.Cmd { t.Fatal("stop saved preferences"); return nil }
			m.settings.editing = &interaction.SettingField{Kind: interaction.SettingInfo, Label: "Details", Description: "Read-only while running"}
			if !strings.Contains(ansi.Strip(m.settingsPanelView()), "[Stop current run]") {
				t.Fatal("stop control hidden by details")
			}
			if mode == "mouse" {
				l := m.settings.layout
				mouse := tea.Mouse{X: l.x + 3, Y: l.y + l.height - 2, Button: tea.MouseLeft}
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			} else {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
			}
			if !m.cancelRequested || !m.running || m.settings.snapshot.Revision != revision || m.input.Value() != draft || len(m.entries) != 0 {
				t.Fatal("stop changed preferences, conversation or claimed completion early")
			}
			if mode == "preparing" {
				m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{cancel: cancel}}})
			}
			if ctx.Err() == nil || !strings.Contains(ansi.Strip(m.settingsPanelView()), "Stopping") {
				t.Fatal("stop did not reach existing cancellation")
			}
			m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true, err: context.Canceled}}})
			if m.running || m.cancelRequested {
				t.Fatal("completion did not release stop state")
			}
		})
	}
}
