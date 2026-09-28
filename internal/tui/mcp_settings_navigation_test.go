package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestMCPSettingsNavigationAndStaleRedirect(t *testing.T) {
	t.Parallel()
	for _, args := range []string{"", "desktop"} {
		m := panelModel(t, 120, 40)
		snapshot := m.settings.snapshot
		snapshot.Fields = append(snapshot.Fields,
			interaction.SettingField{ID: "mcp.services", Category: "tools", Label: "MCP services", Kind: interaction.SettingAction, Action: &interaction.Command{Name: "mcp", Menu: &interaction.CommandMenu{Title: "MCP services", Options: []interaction.CommandOption{{Label: "Computer Use settings", Arguments: "desktop"}}}}},
			interaction.SettingField{ID: "desktop_enabled", Category: "tools", Label: "Computer Use", Kind: interaction.SettingBool})
		m.commands = append(m.commands, interaction.Command{Name: "mcp", Interactive: true})
		m.runSettingsAction = func(*settingsAction, uint64) tea.Cmd {
			t.Fatal("navigation unexpectedly started a settings operation")
			return nil
		}
		m.closeSettings()
		m.running = true
		m.input.SetValue("/mcp add")
		if m.isInfoCommandInput() {
			t.Fatal("MCP mutation classified as local navigation")
		}
		m.input.SetValue("/mcp " + args)
		if !m.isInfoCommandInput() {
			t.Fatal("MCP navigation unavailable during active Run")
		}
		next, _, handled := m.handleComposerAction(inputActionMatch{action: inputActionSend})
		if !handled || next.settings == nil {
			t.Fatal("MCP did not open settings")
		}
		if !next.running || next.cancelRequested || len(next.entries) != 0 || len(next.promptHistory) != 0 || next.submittedInput != nil {
			t.Fatal("MCP navigation changed Run or prompt history")
		}
		m = updateModel(t, next, settingsReadResult{generation: next.settings.generation, snapshot: snapshot})
		if args == "" {
			if m.settings.action == nil || m.settings.action.command.Name != "mcp" {
				t.Fatal("MCP menu not opened")
			}
			a := m.settings.action
			a.running = true
			a.cancel = func() {}
			nextModel, command := m.applySettingActionDone(settingsActionDone{action: a, result: interaction.SettingsActionResult{FocusSetting: "desktop_enabled"}, snapshot: snapshot})
			m = nextModel.(model)
			if command == nil || !m.settings.loading {
				t.Fatal("redirect did not refresh existing settings")
			}
			m = updateModel(t, m, settingsReadResult{generation: m.settings.generation, snapshot: snapshot})
		}
		if fields := m.settings.fields(); m.settings.action != nil || fields[m.settings.selection].ID != "desktop_enabled" {
			t.Fatal("did not focus shared Computer Use setting")
		}
		// A completed/cancelled old action cannot redirect a later panel.
		m = updateModel(t, m, settingsActionDone{action: &settingsAction{}, result: interaction.SettingsActionResult{FocusSetting: "model"}, err: context.Canceled})
		if m.settings.fields()[m.settings.selection].ID != "desktop_enabled" {
			t.Fatal("stale navigation changed panel")
		}
		a := &settingsAction{cancelled: true}
		m.settings.action = a
		m = updateModel(t, m, settingsActionDone{action: a, result: interaction.SettingsActionResult{FocusSetting: "model"}})
		if m.settings.focusField != "" || m.settings.fields()[m.settings.selection].ID != "desktop_enabled" {
			t.Fatal("cancelled navigation changed panel")
		}
	}
}
