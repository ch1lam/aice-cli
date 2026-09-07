package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestGuardLongContentKeepsOptionsVisible(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: strings.Repeat("echo 审核内容\n", 100) + "COMMAND_END",
			Options: guardTestOptions(),
		})
		current = updateModel(t, current, size)
		check := func() {
			t.Helper()
			view := guardViewText(current)
			if lipgloss.Height(view) > size.Height || lipgloss.Width(view) > size.Width {
				t.Fatalf("view exceeds %dx%d: %dx%d", size.Width, size.Height, lipgloss.Width(view), lipgloss.Height(view))
			}
			for _, want := range []string{"1. Allow once", "4. Deny"} {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q:\n%s", want, view)
				}
			}
		}
		check()
		seenEnd := false
		for {
			check()
			seenEnd = seenEnd || strings.Contains(guardViewText(current), "COMMAND_END")
			if current.guardViewport.AtBottom() {
				break
			}
			current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		if !seenEnd {
			t.Fatal("cannot review end of command")
		}
	}
}

func TestGuardReviewPreservesLongText(t *testing.T) {
	for _, command := range []string{strings.Repeat("甲乙abc", 200), strings.Repeat("echo ok\n", 100)} {
		current := newGuardTestModel(t, interaction.GuardRequest{Command: command, Options: guardTestOptions()[:1]})
		current = updateModel(t, current, tea.WindowSizeMsg{Width: 39, Height: 10})
		var lines []string
		for {
			visible := strings.Split(ansi.Strip(current.guardViewport.View()), "\n")
			lines = append(lines, strings.TrimRight(visible[0], " "))
			if current.guardViewport.AtBottom() {
				for _, line := range visible[1:] {
					lines = append(lines, strings.TrimRight(line, " "))
				}
				break
			}
			current.guardViewport.ScrollDown(1)
		}
		flat := strings.ReplaceAll(strings.Join(lines, ""), "\n", "")
		if !strings.Contains(flat, strings.ReplaceAll(command, "\n", "")) {
			t.Fatal("scrolling lost command text or wide characters")
		}
	}
}

func TestGuardLongOptionsAndScrollIsolation(t *testing.T) {
	path := "/tmp/" + strings.Repeat("directory/", 100) + "secret.env"
	options := guardTestOptions()
	options[2].Label = "Allow directory " + path + " for this session"
	options[1].Detail = path
	current := newGuardTestModel(t, interaction.GuardRequest{Path: path, Options: options})
	initialTranscript := current.viewport.YOffset()
	current = updateModel(t, current, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if current.guardViewport.YOffset() == 0 || current.viewport.YOffset() != initialTranscript || current.guardSelection != 0 {
		t.Fatal("wheel failed to scroll only review content")
	}
	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyEnd})
	offset := current.guardViewport.YOffset()
	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	if current.guardSelection != 1 || current.guardViewport.YOffset() != offset {
		t.Fatal("selection moved review content")
	}
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 12})
	view := guardViewText(current)
	if lipgloss.Height(view) > 12 || lipgloss.Width(view) > 40 || !strings.Contains(view, "4. Deny") {
		t.Fatalf("long option escaped screen:\n%s", view)
	}
	flat := strings.ReplaceAll(ansi.Strip(current.guardViewport.GetContent()), "\n", "")
	if !strings.Contains(flat, options[2].Label) || !strings.Contains(flat, path) {
		t.Fatal("long path/option omitted")
	}
	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyHome})
	if !current.guardViewport.AtTop() {
		t.Fatal("home did not reset scroll")
	}
	current = updateModel(t, current, tea.KeyPressMsg{Code: 'n', Text: "n"})
	current = typeGuardText(t, current, strings.Repeat("反馈", 100))
	if view := guardViewText(current); lipgloss.Height(view) > 12 || !strings.Contains(view, "enter send") {
		t.Fatalf("feedback controls escaped screen:\n%s", view)
	}
	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyEscape})
	current = updateModel(t, current, tea.KeyPressMsg{Code: '1', Text: "1"})
	if current.guardPending != nil {
		t.Fatal("allow did not dismiss prompt")
	}
	current = updateModel(t, current, guardRequestMsg{req: &interaction.GuardRequest{Command: "ls", Options: guardTestOptions()}})
	if !current.guardViewport.AtTop() {
		t.Fatal("new request inherited scroll")
	}
}
