package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

type panelFixture struct {
	save func(interaction.SettingsRequest) (interaction.SettingsResult, error)
}

func (panelFixture) ReadSettings(context.Context) (interaction.SettingsSnapshot, error) {
	return interaction.SettingsSnapshot{Revision: 3, SavePath: "/tmp/settings.json", Categories: []interaction.SettingCategory{{ID: "models", Label: "Models & Accounts"}, {ID: "tools", Label: "Tools & Network"}, {ID: "limits", Label: "Run Limits"}, {ID: "project", Label: "Project & Trust"}, {ID: "system", Label: "System"}}, Fields: []interaction.SettingField{
		{ID: "model", Category: "models", Label: "Model", Kind: interaction.SettingEnum, Value: interaction.SettingValue{Kind: interaction.SettingEnum, Text: "first"}, Choices: []interaction.SettingChoice{{Value: "first", Label: "First"}, {Value: "second", Label: "Second"}}},
		{ID: "context_windows", Category: "models", Label: "Context windows", Kind: interaction.SettingContexts, Value: interaction.SettingValue{Kind: interaction.SettingContexts}},
		{ID: "run_timeout", Category: "limits", Label: "Run timeout", Kind: interaction.SettingDuration, Description: "中文 timeout", Value: interaction.SettingValue{Kind: interaction.SettingDuration, Duration: time.Second}},
	}}, nil
}
func (f panelFixture) ApplySettings(_ context.Context, r interaction.SettingsRequest) (interaction.SettingsResult, error) {
	if f.save != nil {
		return f.save(r)
	}
	return interaction.SettingsResult{Committed: true, Applied: true, Applies: interaction.SettingNextRun}, nil
}
func panelModel(t *testing.T, w, h int) model {
	t.Helper()
	m := newModel(make(chan runRequest), make(chan struct{}), SlashCommand{Name: "settings"})
	m = updateModel(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	var close func()
	m.readSettings, m.writeSettings, close = settingsCommands(t.Context(), panelFixture{}, panelFixture{})
	t.Cleanup(close)
	m.input.SetValue("草稿 👩🏽‍💻")
	m, cmd, _ := m.openSettings()
	return updateModel(t, m, cmd())
}
func TestSettingsModalIsolationAndLayout(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 24}, {120, 40}, {240, 60}, {20, 8}} {
		t.Run(string(rune(size[0])), func(t *testing.T) {
			m := panelModel(t, size[0], size[1])
			draft := m.input.Value()
			generation := m.settings.generation
			view := m.View().Content
			if size[0] >= 24 {
				if !strings.Contains(ansi.Strip(view), "Models & Accounts") {
					t.Fatal("missing category")
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
					t.Fatalf("overflow %dx%d: %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
				}
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
			m = updateModel(t, m, tea.PasteMsg{Content: "中文"})
			if m.input.Value() != draft || !strings.Contains(m.settings.input.Value(), "中文") {
				t.Fatal("paste escaped modal")
			}
			m.closeSettings()
			m = updateModel(t, m, settingsReadResult{generation: generation, snapshot: interaction.SettingsSnapshot{SavePath: "stale"}})
			if m.settings != nil || m.input.Value() != draft || len(m.entries) != 0 || len(m.promptHistory) != 0 {
				t.Fatal("modal changed conversation")
			}
		})
	}
}
func TestSettingsErrorRetainsDraftAndReenablesEditor(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 120, 40)
	m.settings.tab = 2
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.settings.input.SetValue("2m3.000000001s")
	next, cmd := m.handleSettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.settings.saving {
		t.Fatal("not submitted")
	}
	m = updateModel(t, m, settingsSaveResult{generation: m.settings.generation, err: errors.New("disk busy")})
	if m.settings.input.Value() != "2m3.000000001s" || !m.settings.input.Focused() {
		t.Fatal("draft lost or editor left disabled")
	}
}
func TestSettingsCollectionAtomicAndMouseEnum(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 120, 40)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	l := m.settings.layout
	mouse := tea.Mouse{X: l.x + 4, Y: l.y + 4, Button: tea.MouseLeft}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	next, cmd := m.Update(tea.MouseReleaseMsg(mouse))
	m = next.(model)
	if cmd == nil || m.settings.choice != 1 {
		t.Fatal("mouse enum did not submit second choice")
	}
	m.settings.saving = false
	m.settings.editing = nil
	m.settings.selection = 1
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	for i, value := range []string{"custom", "精确/模型.ID", "32768"} {
		m.settings.input.SetValue(value)
		if i < 2 {
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		}
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.settings.saving || len(m.settings.collection.value.Contexts) != 1 {
		t.Fatal("row wasn't staged independently")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.settings.collection.leaving {
		t.Fatal("unsaved form discarded without choice")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	next, cmd = m.handleSettings(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.saving {
		t.Fatal("collection did not save")
	}
}
func TestSettingsGuardPreemptsAndSaveAfterClose(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 80, 24)
	p := m.settings
	m.guardPending = &interaction.GuardRequest{ID: "guard"}
	if m.settingsVisible() || m.inputContext().domain != inputGuard {
		t.Fatal("settings hid permission request")
	}
	m.guardPending = nil
	m.closeSettings()
	m = updateModel(t, m, settingsSaveResult{generation: p.generation, snapshot: interaction.SettingsSnapshot{Runtime: interaction.RuntimeState{Model: interaction.DisplayModel{ID: "new-model"}}}})
	if m.currentModel.ID != "new-model" || m.settings != nil {
		t.Fatal("closed modal swallowed committed state")
	}
}

func TestSettingsCursorTracksUnicodeEditorAndCell(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 120, 40)
	m.settings.tab = 2
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.settings.input.SetValue("中文🙂")
	m.settings.input.CursorEnd()
	cursor := m.View().Cursor
	if cursor == nil {
		t.Fatal("missing native cursor")
	}
	l := m.settings.layout
	local := sessionPickerTextCursor(m.settings.input)
	if cursor.X != l.x+2+local.X || cursor.Y != l.y+4 {
		t.Fatalf("cursor=%+v", cursor)
	}
}

type actionFixture struct{ stopped chan struct{} }

func (f actionFixture) RunSettingsAction(ctx context.Context, _ uint64, request interaction.CommandRequest) (string, error) {
	defer close(f.stopped)
	if err := request.Auth.Notify(ctx, interaction.AuthPrompt{Title: "Synthetic credential", AllowInput: true}); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-request.Auth.Input:
		return "Configured synthetic account", nil
	}
}
func TestSettingsActionCancelKeepsConversationAndWaits(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 120, 40)
	draft := m.input.Value()
	stopped := make(chan struct{})
	start, shutdown := settingsActionCommands(t.Context(), actionFixture{stopped: stopped}, panelFixture{})
	t.Cleanup(shutdown)
	a := &settingsAction{request: interaction.CommandRequest{Name: "login"}, running: true}
	m.settings.action = a
	m.settings.editing = &interaction.SettingField{Kind: interaction.SettingAction}
	commands := start(a, 3)().(tea.BatchMsg)
	results := make(chan tea.Msg, 2)
	for _, command := range commands {
		go func(cmd tea.Cmd) { results <- cmd() }(command)
	}
	select {
	case message := <-results:
		m = updateModel(t, m, message)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not arrive")
	}
	m.settings.input.SetValue("synthetic-secret")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case message := <-results:
		m = updateModel(t, m, message)
	case <-time.After(5 * time.Second):
		t.Fatal("action did not cancel")
	}
	shutdown()
	select {
	case <-stopped:
	default:
		t.Fatal("query owner did not wait")
	}
	if m.settings.action != nil || m.settings.input.Value() != "" || m.input.Value() != draft || len(m.entries) != 0 || len(m.promptHistory) != 0 {
		t.Fatal("credential action escaped its input domain")
	}
}
