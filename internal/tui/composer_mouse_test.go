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

func TestToolPathStyleSurvivesHover(t *testing.T) {
	m := foldTestModel()
	m.entries[2].toolDetail = "目录/README.md"
	m.refreshViewport(false)
	before := m.View().Content
	m = updateModel(t, m, tea.MouseMotionMsg(paintedMouse(t, m, "✓ read")))
	after := m.View().Content
	for _, view := range []string{before, after} {
		if !strings.Contains(view, "4:5") || !strings.Contains(view, "38;2;201;160;99") {
			t.Fatal("tool path must keep its gold dashed underline")
		}
	}
	for _, row := range strings.Split(after, "\n") {
		if strings.Contains(ansi.Strip(row), "README.md") && strings.Contains(row, "48;") {
			t.Fatal("hover introduced a background color")
		}
	}
}
