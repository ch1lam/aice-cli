package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type settingsStatusFixture func(context.Context, uint64) (interaction.SettingField, error)

func (f settingsStatusFixture) ReadSettingsStatus(ctx context.Context, revision uint64) (interaction.SettingField, error) {
	return f(ctx, revision)
}

func statusTestField(text string) interaction.SettingField {
	return interaction.SettingField{ID: "desktop.status", Category: "tools", Label: "Computer Use status", Kind: interaction.SettingInfo,
		Value: interaction.SettingValue{Text: text}, Description: text}
}

func TestSettingsStatusUpdatesOnlyItsRowAndOpenDetails(t *testing.T) {
	for _, detail := range []bool{false, true} {
		t.Run(map[bool]string{false: "editing", true: "status-details"}[detail], func(t *testing.T) {
			m := panelModel(t, 120, 40)
			p := m.settings
			p.snapshot.Fields = append(p.snapshot.Fields, statusTestField("Checking…"))
			p.tab, p.selection = 2, 0
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			p.input.SetValue("3m12s")
			p.notice = "Retained notice"
			p.detailOffset = 1
			if detail {
				field := statusTestField("Checking…")
				p.editing = &field
			}
			before := p.snapshot.Fields
			m = updateModel(t, m, settingsStatusResult{generation: p.generation, revision: p.snapshot.Revision, field: statusTestField("Connected")})
			if before[len(before)-1].Value.Text != "Checking…" || p.snapshot.Fields[len(before)-1].Value.Text != "Connected" {
				t.Fatal("status did not replace only its immutable row")
			}
			if p.tab != 2 || p.selection != 0 || p.input.Value() != "3m12s" || p.notice != "Retained notice" || p.snapshot.Revision != 3 || p.detailOffset != 1 {
				t.Fatal("status reset foreground state")
			}
			if detail && p.editing.Description != "Connected" {
				t.Fatal("open status details did not refresh")
			}
			if !detail && p.editing.ID != "run_timeout" {
				t.Fatal("status replaced preference editor")
			}
			if m.input.Value() != "草稿 👩🏽‍💻" || len(m.entries) != 0 {
				t.Fatal("status changed conversation")
			}
		})
	}
}

func TestSettingsStatusCancellationAndLateResults(t *testing.T) {
	for _, action := range []string{"close", "refresh", "save", "domain-action", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			m := panelModel(t, 120, 40)
			p := m.settings
			p.snapshot.Fields = append(p.snapshot.Fields, statusTestField("Checking…"))
			started := make(chan struct{})
			finished := make(chan tea.Msg, 1)
			var shutdown func()
			m.readSettingsStatus, shutdown = settingsStatusCommands(t.Context(), settingsStatusFixture(func(ctx context.Context, revision uint64) (interaction.SettingField, error) {
				close(started)
				<-ctx.Done()
				// Simulate native work that finished at cancellation and still
				// returned success. Generation checks must reject its result.
				return statusTestField("Obsolete"), nil
			}))
			t.Cleanup(shutdown)
			cmd := m.refreshSettingsStatus()
			go func() { finished <- cmd() }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("status did not start")
			}
			switch action {
			case "close":
				m.closeSettings()
				m, _, _ = m.openSettings()
				m.settings.snapshot = p.snapshot
				p = m.settings
			case "refresh":
				m.refreshSettings()
			case "save":
				m.submitSetting(interaction.SettingChange{ID: "run_timeout", Value: interaction.SettingValue{Kind: interaction.SettingDuration, Duration: time.Minute}})
			case "domain-action":
				m.runSettingsAction = func(*settingsAction, uint64) tea.Cmd { return nil }
				next, _ := m.openSettingAction(interaction.SettingField{Kind: interaction.SettingAction, Action: &interaction.Command{Name: "desktop"}})
				m = next.(model)
			case "shutdown":
				shutdown()
			}
			select {
			case msg := <-finished:
				if action != "shutdown" {
					m = updateModel(t, m, msg)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("native read was not cancelled")
			}
			if p.snapshot.Fields[len(p.snapshot.Fields)-1].Value.Text != "Checking…" {
				t.Fatal("late status overwrote the panel")
			}
			shutdown()
			cmd, stop := m.readSettingsStatus(99, 3)
			defer stop()
			if result := cmd().(settingsStatusResult); !errors.Is(result.err, context.Canceled) {
				t.Fatal("query ran after shutdown")
			}
		})
	}
}

func TestSettingsStatusRevisionAndErrorIsolation(t *testing.T) {
	m := panelModel(t, 120, 40)
	p := m.settings
	p.snapshot.Fields = append(p.snapshot.Fields, statusTestField("Checking…"))
	p.notice = "Preference saved"
	m = updateModel(t, m, settingsStatusResult{generation: p.generation, revision: p.snapshot.Revision - 1, field: statusTestField("Old")})
	if p.snapshot.Fields[len(p.snapshot.Fields)-1].Value.Text != "Checking…" {
		t.Fatal("stale revision applied")
	}
	m = updateModel(t, m, settingsStatusResult{generation: p.generation, revision: p.snapshot.Revision, field: statusTestField("Checking…"), err: interaction.ErrSettingsStale})
	field := p.snapshot.Fields[len(p.snapshot.Fields)-1]
	if field.Value.Text != "Unknown" || !strings.Contains(field.Description, "Refresh to retry") || p.notice != "Preference saved" || p.loading {
		t.Fatal("failed status hid preferences or save feedback")
	}
}

func TestSettingsStatusRestartsAfterSaveAndDomainAction(t *testing.T) {
	for _, completion := range []string{"save", "failed-save", "action", "cancel-menu"} {
		t.Run(completion, func(t *testing.T) {
			m := panelModel(t, 120, 40)
			p := m.settings
			calls := 0
			m.readSettingsStatus = func(generation, revision uint64) (tea.Cmd, context.CancelFunc) {
				calls++
				if generation != p.generation || revision != p.snapshot.Revision {
					t.Fatal("status used an obsolete snapshot")
				}
				return func() tea.Msg { return nil }, func() {}
			}
			snapshot := p.snapshot
			snapshot.Revision++
			var cmd tea.Cmd
			switch completion {
			case "save", "failed-save":
				var err error
				if completion == "failed-save" {
					err = errors.New("disk busy")
				}
				_, cmd = m.applySettingsSave(settingsSaveResult{generation: p.generation, snapshot: snapshot, err: err})
			case "action":
				p.action = &settingsAction{}
				_, cmd = m.applySettingActionDone(settingsActionDone{action: p.action, snapshot: snapshot})
			case "cancel-menu":
				p.action = &settingsAction{}
				_, cmd = m.settingActionKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			if calls != 1 || cmd == nil || p.loading {
				t.Fatal("completion did not restart status independently")
			}
		})
	}
}
