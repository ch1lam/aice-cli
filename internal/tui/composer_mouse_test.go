package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestComposerBorderInteraction(t *testing.T) {
	m := foldTestModel()
	idle := m.composerView(m.width)
	mouse := paintedMouse(t, m, defaultPlaceholder)
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	if m.composerView(m.width) == idle {
		t.Fatal("composer hover did not highlight the border")
	}
	outside := paintedMouse(t, m, "✓ read")
	m = updateModel(t, m, tea.MouseMotionMsg(outside))
	if m.composerView(m.width) != idle {
		t.Fatal("hover remained after leaving the composer")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	m = updateModel(t, m, tea.MouseMotionMsg(outside))
	if m.composerView(m.width) == idle {
		t.Fatal("clicked composer lost its active border")
	}
	m = updateModel(t, m, tea.MouseClickMsg(outside))
	m = updateModel(t, m, tea.MouseReleaseMsg(outside))
	if m.composerView(m.width) != idle {
		t.Fatal("outside click did not deactivate composer")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !m.composerActive || m.input.Value() != "x" {
		t.Fatal("typing must activate and edit the composer")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.composerActive || m.input.Value() != "x" {
		t.Fatal("escape should quiet the border without clearing the draft")
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "paste"})
	if !m.composerActive {
		t.Fatal("paste did not activate composer")
	}
	m = updateModel(t, m, tea.BlurMsg{})
	if m.composerActive || m.composerHovered(m.width) {
		t.Fatal("terminal blur retained composer highlight")
	}
}

func TestToolPathStyleFollowsHoverAndExpansion(t *testing.T) {
	for _, name := range []string{"read", "ls", "find", "grep", "write", "edit", "skill"} {
		t.Run(name, func(t *testing.T) {
			m := foldTestModel()
			m.entries[2].toolName = name
			m.entries[2].toolDetail = "目录/README.md"
			m.refreshViewport(false)
			assertTargetStyle := func(gold bool) {
				t.Helper()
				for _, row := range strings.Split(m.View().Content, "\n") {
					if !strings.Contains(ansi.Strip(row), "✓ "+name+"  目录/README.md") {
						continue
					}
					if strings.Contains(row, "4:5") != gold || strings.Contains(row, "38;2;201;160;99") != gold {
						t.Fatalf("target gold/underline = %v expected: %q", gold, row)
					}
					if strings.Contains(row, "\x1b[48;") || strings.Contains(row, ";48;") {
						t.Fatal("target introduced a background color")
					}
					return
				}
				t.Fatal("missing tool heading")
			}
			assertTargetStyle(false)
			heading := "✓ " + name + "  目录/README.md"
			m = updateModel(t, m, tea.MouseMotionMsg(paintedMouse(t, m, heading)))
			assertTargetStyle(true)
			m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
			assertTargetStyle(false)
			m = clickPainted(t, m, heading)
			m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
			assertTargetStyle(true)
			m = clickPainted(t, m, heading)
			assertTargetStyle(true)
			m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
			assertTargetStyle(false)
		})
	}
}
