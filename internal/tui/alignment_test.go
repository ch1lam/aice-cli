package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestTranscriptAlignmentAcrossFoldsAndWidths(t *testing.T) {
	for _, width := range []int{24, 40, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := newModel(nil, nil)
			id := m.beginProcess()
			m.entries = []transcriptEntry{
				{kind: entryUser, text: "USER_TEXT"},
				{kind: entryAssistant, processID: id, thinking: "THINKING_TEXT", text: "PROGRESS_TEXT", complete: true},
				{kind: entryTool, processID: id, toolName: "bash", toolDetail: "echo done", toolDone: true,
					toolOutput: interaction.ToolOutputDisplay{Available: true, Text: "done"}},
				{kind: entryAssistant, processID: id, text: "FINAL_TEXT", complete: true, conclusion: true},
			}
			m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 60})
			for _, state := range []string{"default", "expanded", "batch closed", "process closed", "reopened"} {
				switch state {
				case "expanded":
					m.expandAllDetails(true)
				case "batch closed":
					m.setFoldExpanded(foldTarget{kind: foldCalls, id: 2}, false)
				case "process closed":
					m.setFoldExpanded(foldTarget{kind: foldProcess, id: id}, false)
				case "reopened":
					m.setFoldExpanded(foldTarget{kind: foldProcess, id: id}, true)
					m.setFoldExpanded(foldTarget{kind: foldCalls, id: 2}, true)
				}
				m.refreshViewport(false)
				m.viewport.GotoTop()
				view := ansi.Strip(m.View().Content)
				markers := []string{"USER_TEXT", "FINAL_TEXT"}
				if state != "process closed" {
					markers = append(markers, "PROGRESS_TEXT")
				}
				if state != "default" && state != "process closed" {
					markers = append(markers, "THINKING_TEXT")
				}
				if state == "expanded" || state == "reopened" {
					markers = append(markers, "$ echo done")
				}
				for _, marker := range markers {
					assertTextColumn(t, view, marker, m.horizontalPadding()+3)
				}
				for _, row := range m.viewport.visibleRows() {
					if row.fold.kind == foldNone {
						continue
					}
					text := ansi.Strip(row.text)
					if got := len(text) - len(strings.TrimLeft(text, " ")); got != 3 {
						t.Fatalf("%s fold column = %d: %q", state, got, text)
					}
				}
			}
		})
	}
}

func TestExpandedPathContinuationKeepsAlignmentAndHover(t *testing.T) {
	m := foldTestModel()
	m.entries[2].toolDetail = "/workspace/" + strings.Repeat("目录🙂/", 15) + "main.go"
	m.setFoldExpanded(foldTarget{kind: foldTool, id: 2}, true)
	for _, width := range []int{24, 40, 100} {
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 60})
		header := m.foldedToolItems(2)[0]
		view := header.render()
		if ansi.Strip(view) != ansi.Strip(header.hoverText) {
			t.Fatal("hover changed expanded path wrapping")
		}
		rows := strings.Split(view, "\n")
		if len(rows) < 2 {
			t.Fatal("fixture did not wrap")
		}
		for _, row := range rows {
			plain := ansi.Strip(row)
			if !strings.HasPrefix(plain, "   ") || strings.HasPrefix(plain, "    ") {
				t.Fatalf("continuation lost left edge: %q", plain)
			}
			if ansi.StringWidth(row) > m.layoutWidth() {
				t.Fatalf("heading overflows at %d columns: %q", width, plain)
			}
		}
	}
}

func assertTextColumn(t *testing.T, view, marker string, want int) {
	t.Helper()
	for _, row := range strings.Split(view, "\n") {
		if before, _, ok := strings.Cut(row, marker); ok {
			if got := ansi.StringWidth(before); got != want {
				t.Fatalf("%s starts at %d, want %d: %q", marker, got, want, row)
			}
			return
		}
	}
	t.Fatalf("missing %s in:\n%s", marker, view)
}
