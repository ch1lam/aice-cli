package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

// Locate what View actually paints, including header/composer offsets. Tests
// must not invent hit rectangles independently of the displayed frame.
func paintedMouse(t *testing.T, m model, text string) tea.Mouse {
	t.Helper()
	for y, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(row, text) {
			return tea.Mouse{X: m.viewport.Width() - 1, Y: y, Button: tea.MouseLeft}
		}
	}
	t.Fatalf("no painted row %q in:\n%s", text, ansi.Strip(m.View().Content))
	return tea.Mouse{}
}

func clickPainted(t *testing.T, m model, text string) model {
	t.Helper()
	mouse := paintedMouse(t, m, text)
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	updated, command := m.Update(tea.MouseReleaseMsg(mouse))
	if command != nil {
		t.Fatal("fold click unexpectedly copied text")
	}
	return updated.(model)
}

func TestMouseFoldsAtPaintedRowsAndRetainsAnchor(t *testing.T) {
	for _, width := range []int{24, 40, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := foldTestModel()
			m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 36})
			m.viewport.GotoTop()
			mouse := paintedMouse(t, m, "✓ read")
			before := m.View().Content
			m = updateModel(t, m, tea.MouseMotionMsg(mouse))
			after := m.View().Content
			if before == after || ansi.Strip(before) != ansi.Strip(after) {
				t.Fatal("hover must change only the row style")
			}
			if m.foldExpanded(foldTarget{kind: foldTool, id: 2}) {
				t.Fatal("hover expanded the tool")
			}
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			if strings.Contains(ansi.Strip(m.View().Content), "README_BODY") {
				t.Fatal("press activated before release")
			}
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if !strings.Contains(ansi.Strip(m.View().Content), "README_BODY") {
				t.Fatal("click did not expand recorded output")
			}
			if now := paintedMouse(t, m, "✓ read"); now.Y != mouse.Y {
				t.Fatal("clicked header jumped")
			}
			m = clickPainted(t, m, "1 skill")
			if strings.Contains(ansi.Strip(m.View().Content), "README_BODY") {
				t.Fatal("group did not collapse")
			}
			m = clickPainted(t, m, "1 skill")
			if !strings.Contains(ansi.Strip(m.View().Content), "README_BODY") || strings.Contains(ansi.Strip(m.View().Content), "SKILL_BODY") {
				t.Fatal("parent reset children")
			}
			m = clickPainted(t, m, "ctrl+o")
			if !strings.Contains(ansi.Strip(m.View().Content), "FINAL_ANSWER") || strings.Contains(ansi.Strip(m.View().Content), "README_BODY") {
				t.Fatalf("process fold swallowed answer or leaked details: expanded=%v\n%s", m.foldExpanded(foldTarget{kind: foldProcess, id: 1}), ansi.Strip(m.View().Content))
			}
			m = clickPainted(t, m, "ctrl+o")
			m = clickPainted(t, m, "Thinking")
			if !strings.Contains(ansi.Strip(m.View().Content), "REASONING_BODY") {
				t.Fatal("thinking click failed")
			}
		})
	}
}

func TestMouseDragAndCancelledPressNeverToggleFold(t *testing.T) {
	m := foldTestModel()
	mouse := paintedMouse(t, m, "✓ read")
	mouse.X = 6
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	drag := mouse
	drag.X += 4
	m = updateModel(t, m, tea.MouseMotionMsg(drag))
	updated, command := m.Update(tea.MouseReleaseMsg(drag))
	m = updated.(model)
	if m.foldExpanded(foldTarget{kind: foldTool, id: 2}) || command == nil || !m.copyNotice {
		t.Fatal("drag must copy without folding")
	}
	for _, cancel := range []tea.Msg{tea.BlurMsg{}, tea.WindowSizeMsg{Width: 100, Height: 32}, tea.MouseWheelMsg{Button: tea.MouseWheelDown}} {
		m = foldTestModel()
		mouse = paintedMouse(t, m, "✓ read")
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		m = updateModel(t, m, cancel)
		m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
		if m.foldExpanded(foldTarget{kind: foldTool, id: 2}) {
			t.Fatalf("cancelled press toggled after %T", cancel)
		}
	}
}

func TestMouseHoverRecomputesAfterScrollAndBlocksOtherSurfaces(t *testing.T) {
	m := foldTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 16})
	m.viewport.GotoTop()
	mouse := paintedMouse(t, m, "✓ read")
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	if m.hoveredFold().kind != foldTool {
		t.Fatal("missing tool hover")
	}
	m.viewport.scroll(2)
	if m.hoveredFold() == (foldTarget{kind: foldTool, id: 2}) {
		t.Fatal("hover retained pre-scroll target")
	}
	for _, surface := range []string{"side", "guard", "composer", "outside"} {
		t.Run(surface, func(t *testing.T) {
			m := foldTestModel()
			mouse := paintedMouse(t, m, "✓ read")
			switch surface {
			case "side":
				m.side.isVisible = true
			case "guard":
				m.guardPending = &interaction.GuardRequest{}
			case "composer":
				mouse.Y = m.height - 2
			case "outside":
				mouse.X = m.width
			}
			if m.foldHitAt(mouse).target.kind != foldNone {
				t.Fatal("hit leaked through another surface")
			}
		})
	}
}

func TestHoverDoesNotFormatOffscreenOrCollapsedBodies(t *testing.T) {
	m := foldTestModel()
	calls := 0
	m.viewport.items = append(m.viewport.items, transcriptItem{key: 9999, version: 1, render: func() string {
		calls++
		return "OFFSCREEN_BODY"
	}})
	m.viewport.height = 4
	m.viewport.GotoTop()
	for i := range 100 {
		m = updateModel(t, m, tea.MouseMotionMsg{X: i % 40, Y: 3})
		m.View()
	}
	if calls != 0 {
		t.Fatal("mouse movement formatted offscreen content")
	}
}

func TestFoldReadingPositionSurvivesRunCompletion(t *testing.T) {
	m := foldTestModel()
	m.running = true
	m.entries[2].toolOutput.Text = strings.Repeat("long output line\n", 100)
	m = clickPainted(t, m, "✓ read")
	before := paintedMouse(t, m, "✓ read")
	if m.viewport.AtBottom() {
		t.Fatal("fixture should be reading historical output")
	}
	m.finishRun(nil)
	after := paintedMouse(t, m, "✓ read")
	if before.Y != after.Y || m.viewport.AtBottom() {
		t.Fatal("completion jumped away from expanded output")
	}
}

func TestMouseHitsWrappedUnicodeHeadingsButNotTheirBody(t *testing.T) {
	m := foldTestModel()
	m.entries[2].toolDetail = "中文🙂文件.md"
	m.entries[2].toolOutput.Text = strings.Repeat("中文🙂输出内容\n", 15)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 28, Height: 20})
	m.viewport.GotoTop()
	m = clickPainted(t, m, "✓ read")
	body := paintedMouse(t, m, "中文🙂输出内容")
	if m.foldHitAt(body).target.kind != foldNone {
		t.Fatal("body became a fold heading")
	}
	m = updateModel(t, m, tea.MouseClickMsg(body))
	m = updateModel(t, m, tea.MouseReleaseMsg(body))
	if !m.foldExpanded(foldTarget{kind: foldTool, id: 2}) {
		t.Fatal("body click folded its parent")
	}
	// A permission prompt arriving between press and release must cancel it.
	m.viewport.GotoTop()
	mouse := paintedMouse(t, m, "✓ read")
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, guardRequestMsg{req: &interaction.GuardRequest{Reply: make(chan interaction.GuardReply, 1)}})
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	if m.selection.active || !m.foldExpanded(foldTarget{kind: foldTool, id: 2}) {
		t.Fatal("permission prompt retained a pending fold click")
	}
}

func TestTerminalSGRMouseClickExpandsTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := foldTestModel()
	mouse := paintedMouse(t, m, "✓ read")
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(reader), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithWindowSize(100, 32), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	waitForTerminalText(t, ctx, output, "README.md")
	_, err := fmt.Fprintf(writer, "\x1b[<35;%d;%dM\x1b[<0;%d;%dM\x1b[<0;%d;%dm",
		mouse.X+1, mouse.Y+1, mouse.X+1, mouse.Y+1, mouse.X+1, mouse.Y+1)
	if err != nil {
		t.Fatal(err)
	}
	waitForTerminalText(t, ctx, output, "README_BODY")
}
