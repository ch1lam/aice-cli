package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func sectionPanelModel(t *testing.T, width, height int) model {
	t.Helper()
	m := panelModel(t, width, height)
	m.settings.tab = 1
	m.settings.snapshot.Fields = nil
	for _, prefix := range []string{"browser.", "desktop.", "web.search."} {
		for i := range 3 {
			id := fmt.Sprintf("%s%d", prefix, i)
			value := interaction.SettingValue{Kind: interaction.SettingString, Text: "value"}
			m.settings.snapshot.Fields = append(m.settings.snapshot.Fields, interaction.SettingField{
				ID: id, Category: "tools", Label: id, Kind: interaction.SettingString,
				Value: value, Default: &value, Inherited: &value,
			})
		}
	}
	return m
}

func clickSettingSection(t *testing.T, m model, title string) model {
	t.Helper()
	mouse := settingsPaintedMouse(t, m, title)
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	return updateModel(t, m, tea.MouseReleaseMsg(mouse))
}

func TestSettingsSectionMouseCollapseAndHover(t *testing.T) {
	t.Parallel()
	m := sectionPanelModel(t, 120, 40)
	initial := m.settingsPanelView()
	for _, label := range []string{"browser.0", "desktop.0", "web.search.0"} {
		if !strings.Contains(ansi.Strip(initial), label) {
			t.Fatalf("section starts collapsed: missing %s", label)
		}
	}
	mouse := settingsPaintedMouse(t, m, "Browser")
	// The rule to the right of the title is part of the same click target.
	mouse.X = m.settings.layout.x + m.settings.layout.width - 3
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	hover := m.settingsPanelView()
	if hover == initial || ansi.Strip(hover) != ansi.Strip(initial) {
		t.Fatal("hover must change styling without changing text or layout")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	if m.settingsPanelView() != hover {
		t.Fatal("press added styling beyond hover")
	}
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	view := ansi.Strip(m.settingsPanelView())
	if strings.Contains(view, "browser.0") || !strings.Contains(view, "Browser") || !strings.Contains(view, "desktop.0") {
		t.Fatal("click did not collapse exactly the Browser section")
	}
	if m.settings.editing != nil || m.settings.saving {
		t.Fatal("folding a section edited a setting")
	}
	m = clickSettingSection(t, m, "Browser")
	if !strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("second click did not expand section")
	}
	m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
	if m.settingsPanelView() != initial {
		t.Fatal("hover did not clear after leaving the heading")
	}
	// Releasing on another target must cancel the press, including its style.
	mouse = settingsPaintedMouse(t, m, "Browser")
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	mouse.Y++
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	if m.settings.pressed != "" || !strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("dragging off the heading toggled it or left a stuck press")
	}
}

func settingsHeadingLine(t *testing.T, m model, title string) string {
	t.Helper()
	for _, line := range strings.Split(m.settingsPanelView(), "\n") {
		if strings.Contains(ansi.Strip(line), title) {
			return line
		}
	}
	t.Fatalf("missing heading %q", title)
	return ""
}

func TestSettingsSectionHighlightClearsImmediately(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"leave", "blur", "resize", "keyboard"} {
		t.Run(action, func(t *testing.T) {
			m := sectionPanelModel(t, 120, 40)
			expanded := settingsHeadingLine(t, m, "Browser")
			m = clickSettingSection(t, m, "Browser")
			want := strings.Replace(expanded, "  Browser", "› Browser", 1)
			if got := settingsHeadingLine(t, m, "Browser"); got != want {
				t.Fatal("release retained hover or selection highlighting")
			}
			baseline := m.settingsPanelView()
			mouse := settingsPaintedMouse(t, m, "Browser")
			m = updateModel(t, m, tea.MouseMotionMsg(mouse))
			if settingsHeadingLine(t, m, "Browser") == want {
				t.Fatal("fresh motion did not restore hover")
			}
			switch action {
			case "leave":
				m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
			case "blur":
				m = updateModel(t, m, tea.BlurMsg{})
			case "resize":
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
			case "keyboard":
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
			}
			if m.settingsPanelView() != baseline {
				t.Fatal("hover did not clear in the same input update")
			}
		})
	}
}

func TestSettingsSectionFeedbackDoesNotWaitForDesktopStatus(t *testing.T) {
	t.Parallel()
	m := sectionPanelModel(t, 120, 40)
	p := m.settings
	p.snapshot.Fields = append(p.snapshot.Fields, statusTestField("Checking…"))
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan tea.Msg, 1)
	var shutdown func()
	m.readSettingsStatus, shutdown = settingsStatusCommands(t.Context(), settingsStatusFixture(
		func(ctx context.Context, _ uint64) (interaction.SettingField, error) {
			close(started)
			select {
			case <-release:
				return statusTestField("Connected"), nil
			case <-ctx.Done():
				return interaction.SettingField{}, ctx.Err()
			}
		},
	))
	t.Cleanup(shutdown)
	cmd := m.refreshSettingsStatus()
	go func() { finished <- cmd() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("status check did not start")
	}
	expanded := settingsHeadingLine(t, m, "Browser")
	m = clickSettingSection(t, m, "Browser")
	want := strings.Replace(expanded, "  Browser", "› Browser", 1)
	if settingsHeadingLine(t, m, "Browser") != want {
		t.Fatal("release feedback waited for pending status")
	}
	m = updateModel(t, m, tea.MouseMotionMsg(settingsPaintedMouse(t, m, "Browser")))
	m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
	if settingsHeadingLine(t, m, "Browser") != want {
		t.Fatal("hover exit waited for pending status")
	}
	close(release)
	select {
	case result := <-finished:
		m = updateModel(t, m, result)
	case <-time.After(5 * time.Second):
		t.Fatal("status check did not finish")
	}
	if settingsHeadingLine(t, m, "Browser") != want || strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("late status changed section feedback or fold state")
	}
	if p.snapshot.Fields[len(p.snapshot.Fields)-1].Value.Text != "Connected" {
		t.Fatal("status result was not applied")
	}
}

func TestSettingsCollapsedNavigationAndEditing(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {32, 16}, {24, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := sectionPanelModel(t, size[0], size[1])
			for _, id := range []string{"browser.0", "desktop.0", "web.search.0"} {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
				if got := m.settings.fields()[m.settings.selection].ID; got != id {
					t.Fatalf("collapsed selection = %s, want %s", got, id)
				}
				for _, key := range []tea.KeyPressMsg{{Code: '?', Text: "?"}, {Code: 'D', Text: "D"}, {Code: 'u', Text: "u"}} {
					m = updateModel(t, m, key)
					if m.settings.editing != nil || m.settings.saving {
						t.Fatal("hidden field was editable")
					}
				}
				m = updateModel(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			}
			view := m.settingsPanelView()
			if strings.Contains(ansi.Strip(view), "value") {
				t.Fatal("collapsed field is still painted")
			}
			if lipgloss.Width(view) != m.settings.layout.width || lipgloss.Height(view) != m.settings.layout.height {
				t.Fatal("collapsed panel overflowed")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
			if m.settings.fields()[m.settings.selection].ID != "desktop.0" {
				t.Fatal("up did not skip hidden fields")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.settings.editing != nil {
				t.Fatal("Enter should expand a collapsed section before editing")
			}
			mouse := settingsPaintedMouse(t, m, "› des")
			if m.settings.target(mouse) != "field:desktop.0" {
				t.Fatal("expanded field is not the painted selection")
			}
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.settings.editing == nil || m.settings.editing.ID != "desktop.0" {
				t.Fatal("click after expansion opened the wrong setting")
			}
		})
	}
}

func TestSettingsSectionSearchAndLifetime(t *testing.T) {
	t.Parallel()
	m := sectionPanelModel(t, 120, 40)
	m = clickSettingSection(t, m, "Browser")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("switching categories lost the collapsed state")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.PasteMsg{Content: "browser."})
	if !strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("search result stayed hidden by the category fold")
	}
	m = clickSettingSection(t, m, "Tools & Network · Browser")
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("search section did not fold")
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "0"})
	if !strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("new query did not reveal matching fields")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("search changed the category fold")
	}
	snapshot := m.settings.snapshot
	m = updateModel(t, m, settingsReadResult{generation: m.settings.generation, snapshot: snapshot})
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("refresh lost the fold")
	}
	m.settings.focusField = "browser.1"
	m = updateModel(t, m, settingsReadResult{generation: m.settings.generation, snapshot: snapshot})
	if !strings.Contains(ansi.Strip(m.settingsPanelView()), "› browser.1") {
		t.Fatal("explicit navigation did not reveal and select the requested field")
	}
	m = clickSettingSection(t, m, "Browser")
	m.closeSettings()
	m, _, _ = m.openSettings()
	m = updateModel(t, m, settingsReadResult{generation: m.settings.generation, snapshot: snapshot})
	if !strings.Contains(ansi.Strip(m.settingsPanelView()), "browser.0") {
		t.Fatal("reopening settings did not reset to expanded")
	}
}

func TestSettingsRepeatedSectionHeadingCollapsesAfterScrolling(t *testing.T) {
	t.Parallel()
	m := sectionPanelModel(t, 80, 24)
	for i := 3; i < 18; i++ {
		id := fmt.Sprintf("desktop.%d", i)
		m.settings.snapshot.Fields = append(m.settings.snapshot.Fields, interaction.SettingField{
			ID: id, Category: "tools", Label: id, Kind: interaction.SettingString,
		})
	}
	for range 20 {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := ansi.Strip(m.settingsPanelView())
	if !strings.Contains(view, "› desktop.17") || strings.Contains(view, "desktop.0 ") {
		t.Fatal("selected field is not visible after scrolling")
	}
	m = clickSettingSection(t, m, "Computer Use")
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "desktop.") {
		t.Fatal("repeated heading did not collapse its section")
	}
	m = clickSettingSection(t, m, "Computer Use")
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 32, Height: 16})
	for range 2 {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = clickSettingSection(t, m, "Computer Use")
	if strings.Contains(ansi.Strip(m.settingsPanelView()), "desktop.") {
		t.Fatal("resized heading did not collapse its section")
	}
}
