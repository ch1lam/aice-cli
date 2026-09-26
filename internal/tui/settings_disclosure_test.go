package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingsDisclosurePagesBeforeActionAndSharesMouseGeometry(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 24}, {120, 40}, {32, 16}} {
		t.Run(string(rune(size[0])), func(t *testing.T) {
			m := panelModel(t, size[0], size[1])
			input := make(chan string, 1)
			instructions := strings.Repeat("Read the complete disclosure before continuing. ", 12) + "FINAL NOTICE"
			a := &settingsAction{running: true, input: input, wait: func() tea.Msg { return nil }}
			m.settings.action = a
			m.settings.editing = &interaction.SettingField{Kind: interaction.SettingAction}
			menu := &interaction.CommandMenu{Title: "Continue?", Options: []interaction.CommandOption{{Label: "Cancel", Arguments: "cancel"}, {Label: "Continue", Arguments: "continue"}}}
			m = updateModel(t, m, settingsActionPrompt{action: a, prompt: interaction.AuthPrompt{Title: "Enable Computer Use", Instructions: instructions, Menu: menu}})
			var seen []string
			for page := 0; ; page++ {
				if page > 100 {
					t.Fatal("disclosure cannot advance")
				}
				layout := m.settings.actionMenuLayout(menu)
				seen = append(seen, strings.Join(layout.header, ""))
				view := m.settings.actionView()
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.settings.layout.inner {
						t.Fatalf("disclosure overflow: %q", line)
					}
				}
				if !layout.more {
					break
				}
				for row := 0; row < m.settings.layout.bodyHeight; row++ {
					if strings.HasPrefix(m.settings.editTarget(1, row+2), "action:") {
						t.Fatal("action clickable before disclosure ends")
					}
				}
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
				if len(input) != 0 {
					t.Fatal("paging submitted an answer")
				}
			}
			if !strings.Contains(strings.Join(seen, ""), "FINAL NOTICE") {
				t.Fatal("disclosure was clipped")
			}
			layout := m.settings.actionMenuLayout(menu)
			target := m.settings.editTarget(1, 2+len(layout.header)+1)
			if target != "action:Continue:continue" {
				t.Fatalf("wrong hit target: %s", target)
			}
			_, _ = m.settingEditClick(target)
			if answer := <-input; answer != "continue" {
				t.Fatal(answer)
			}
			if len(m.entries) != 0 || len(m.promptHistory) != 0 {
				t.Fatal("setup disclosure entered conversation")
			}
		})
	}
}

func TestSettingsBooleanEnableUsesDomainAction(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 80, 24)
	m.settings.snapshot.Fields = []interaction.SettingField{{ID: "desktop_enabled", Category: "models", Kind: interaction.SettingBool, Value: interaction.SettingValue{Kind: interaction.SettingBool}, Action: &interaction.Command{Name: "desktop"}, Arguments: "setup"}}
	started := false
	m.runSettingsAction = func(a *settingsAction, revision uint64) tea.Cmd {
		started = true
		if a.request.Name != "desktop" || a.request.Arguments != "setup" {
			t.Fatal(a.request)
		}
		return nil
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !started || m.settings.saving || m.settings.action == nil {
		t.Fatal("enable bypassed explicit setup action")
	}
}

func TestSettingsCancelledActionRejectsLateDisclosure(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 80, 24)
	a := &settingsAction{running: true, input: make(chan string, 1)}
	m.settings.action = a
	m.settings.editing = &interaction.SettingField{Kind: interaction.SettingAction}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updateModel(t, m, settingsActionPrompt{action: a, prompt: interaction.AuthPrompt{Title: "Late disclosure", Menu: &interaction.CommandMenu{Options: []interaction.CommandOption{{Label: "Continue", Arguments: "continue"}}}}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.prompt != nil || len(a.input) != 0 || !strings.Contains(m.settings.notice, "Cancelling") {
		t.Fatal("cancelled action reactivated")
	}
}
