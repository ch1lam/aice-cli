package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingRowsAlignValuesAndMuteOff(t *testing.T) {
	t.Parallel()
	for _, width := range []int{16, 24, 72, 108} {
		for _, selected := range []bool{false, true} {
			for _, invert := range []bool{false, true} {
				for _, enabled := range []bool{false, true} {
					field := interaction.SettingField{Label: "窗口显示", Kind: interaction.SettingBool,
						Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: enabled}, InvertBool: invert}
					row := settingRow(field, selected, width)
					value, style := "On", bodyStyle
					if enabled == invert {
						value, style = "Off", mutedStyle
					}
					if ansi.StringWidth(row) != width || strings.Contains(row, "\n") {
						t.Fatalf("row overflow at width %d: %q", width, row)
					}
					if ansi.Cut(ansi.Strip(row), width-2-len(value), width-2) != value {
						t.Fatalf("value is not right-aligned: %q", row)
					}
					if !strings.Contains(row, style.Render(value)) {
						t.Fatalf("wrong boolean value color: %q", row)
					}
				}
			}
		}
		field := interaction.SettingField{Label: strings.Repeat("长名称", 20), Kind: interaction.SettingString,
			Value: interaction.SettingValue{Kind: interaction.SettingString, Text: strings.Repeat("模型🙂", 20)}}
		row := settingRow(field, true, width)
		if ansi.StringWidth(row) != width || strings.Contains(row, "\n") || !strings.Contains(row, "…") {
			t.Fatalf("long label/value overflow at width %d: %q", width, row)
		}
	}
}

func TestSettingsBooleanToggleAndEnumSelection(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"enter", "label", "value"} {
		for _, invert := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invert=%v", mode, invert), func(t *testing.T) {
				m := panelModel(t, 120, 40)
				m.settings.snapshot.Fields = []interaction.SettingField{{ID: "test", Category: "models", Label: "Switch",
					Kind: interaction.SettingBool, Value: interaction.SettingValue{Kind: interaction.SettingBool}, InvertBool: invert}}
				var changes []interaction.SettingChange
				m.writeSettings = func(generation uint64, request interaction.SettingsRequest) tea.Cmd {
					changes = append(changes, request.Changes...)
					snapshot := m.settings.snapshot
					snapshot.Fields = append([]interaction.SettingField(nil), snapshot.Fields...)
					snapshot.Fields[0].Value = request.Changes[0].Value
					return func() tea.Msg { return settingsSaveResult{generation: generation, snapshot: snapshot} }
				}
				for _, want := range []bool{true, false} {
					var next tea.Model
					var cmd tea.Cmd
					if mode == "enter" {
						next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					} else {
						mouse := settingsPaintedMouse(t, m, "Switch")
						if mode == "value" {
							mouse.X = m.settings.layout.x + m.settings.layout.width - 5
						}
						m = updateModel(t, m, tea.MouseClickMsg(mouse))
						next, cmd = m.Update(tea.MouseReleaseMsg(mouse))
					}
					m = next.(model)
					if cmd == nil || m.settings.editing != nil || changes[len(changes)-1].Value.Bool != want {
						t.Fatal("boolean did not toggle directly")
					}
					m = updateModel(t, m, cmd())
					if m.settings.snapshot.Fields[0].Value.Bool != want || m.settings.saving {
						t.Fatal("saved boolean did not return to list")
					}
				}
				if len(changes) != 2 {
					t.Fatalf("toggle generated %d writes", len(changes))
				}
			})
		}
	}
	for _, mode := range []string{"enter", "mouse"} {
		t.Run("enum/"+mode, func(t *testing.T) {
			m := panelModel(t, 120, 40)
			// An inherited choice must display its label and receive menu focus.
			m.settings.snapshot.Fields[0].Value.Text = ""
			m.settings.snapshot.Fields[0].Effective = "second"
			var saved string
			m.writeSettings = func(generation uint64, request interaction.SettingsRequest) tea.Cmd {
				saved = request.Changes[0].Value.Text
				snapshot := m.settings.snapshot
				snapshot.Fields = append([]interaction.SettingField(nil), snapshot.Fields...)
				snapshot.Fields[0].Value = request.Changes[0].Value
				return func() tea.Msg { return settingsSaveResult{generation: generation, snapshot: snapshot} }
			}
			mouse := settingsPaintedMouse(t, m, "Second")
			if mode == "mouse" {
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			} else {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if m.settings.editing == nil || m.settings.choice != 1 || saved != "" {
				t.Fatal("enum did not open a submenu focused on the current value")
			}
			var next tea.Model
			var cmd tea.Cmd
			if mode == "mouse" {
				mouse = settingsPaintedMouse(t, m, "First")
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				next, cmd = m.Update(tea.MouseReleaseMsg(mouse))
			} else {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
				next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			m = next.(model)
			if cmd == nil || saved != "first" {
				t.Fatalf("submenu saved %q", saved)
			}
			m = updateModel(t, m, cmd())
			if m.settings.editing != nil || !strings.Contains(ansi.Strip(m.settingsPanelView()), "First") {
				t.Fatal("saved enum not displayed on the main list")
			}
		})
	}
}

func TestSettingsEnumMouseMatchesVisibleChoices(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{32, 16}, {24, 12}} {
		m := panelModel(t, size[0], size[1])
		field := &m.settings.snapshot.Fields[0]
		field.Choices = nil
		for i := range 20 {
			field.Choices = append(field.Choices, interaction.SettingChoice{Value: fmt.Sprint(i), Label: fmt.Sprintf("Option %02d", i)})
		}
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		for i := range 20 {
			if i > 0 {
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			}
			mouse := settingsPaintedMouse(t, m, fmt.Sprintf("Option %02d", i))
			if got := m.settings.target(mouse); got != "choice:"+fmt.Sprint(i) {
				t.Fatalf("visible choice %d has target %q", i, got)
			}
			mouse.Y++
			if got := m.settings.target(mouse); strings.HasPrefix(got, "choice:") {
				// Before scrolling begins, the next row may contain a real choice.
				lines := strings.Split(ansi.Strip(m.settingsPanelView()), "\n")
				if !strings.Contains(lines[mouse.Y-m.settings.layout.y], "Option") {
					t.Fatal("blank menu row targets a hidden choice")
				}
			}
		}
	}
}
