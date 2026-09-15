package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func codeTestModel(t *testing.T, width int) model {
	t.Helper()
	m := newModel(make(chan runRequest), make(chan struct{}))
	return updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
}

func codeButtonMouse(t *testing.T, m model, source string) tea.Mouse {
	t.Helper()
	for y, row := range m.viewport.visibleRows() {
		if p := row.code.placement; p != nil && row.code.row == 0 && p.layout.block.source == source {
			x := p.column + p.layout.copyColumn
			if got := ansi.Strip(ansi.Cut(row.text, x, x+6)); got != "[Copy]" {
				t.Fatalf("button geometry points at %q", got)
			}
			return tea.Mouse{X: m.horizontalPadding() + x, Y: m.verticalPadding() +
				lipgloss.Height(m.headerView(m.layoutWidth())) + y, Button: tea.MouseLeft}
		}
	}
	t.Fatalf("no visible button for %q: %s", source, ansi.Strip(m.viewport.View()))
	return tea.Mouse{}
}

func assertClipboard(t *testing.T, command tea.Cmd, want string) {
	t.Helper()
	if command == nil {
		t.Fatal("missing clipboard command")
	}
	if got := fmt.Sprint(command().(tea.BatchMsg)[0]()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}

func TestCodeButtonCopiesOriginalAcrossPresentations(t *testing.T) {
	source := "\talpha  \r\n\n中文 `raw`\n"
	markdown := "Intro\n\n> - item\n>\n>   ```text\n>   \talpha  \r\n>   \n>   中文 `raw`\n>   ```\n\nEnd"
	for _, width := range []int{30, 60, 100} {
		for _, kind := range []string{"assistant", "process", "tool", "preview", "side"} {
			t.Run(fmt.Sprintf("%s/%d", kind, width), func(t *testing.T) {
				m := codeTestModel(t, width)
				e := transcriptEntry{kind: entryAssistant, text: markdown, thinking: "thinking", complete: true}
				var item transcriptItem
				switch kind {
				case "assistant":
					item = m.transcriptEntryItem(0, e, false, transcriptStandalone)
				case "process":
					item = m.transcriptEntryItem(0, e, false, transcriptConclusion)
				case "tool":
					e = transcriptEntry{kind: entryTool, toolName: "bash", toolDetail: strings.Repeat("long command ", 10), toolDone: true,
						toolOutput: interaction.ToolOutputDisplay{Text: source, Available: true}}
					item = m.transcriptEntryItem(0, e, false, transcriptStandalone)
				case "preview":
					m.entries = []transcriptEntry{{kind: entryTool, toolName: "write", writePreview: &writePreview{content: source, known: true}}}
					m.setFoldExpanded(foldTarget{kind: foldTool, id: 0}, true)
					item = m.foldedToolItems(0)[1]
				case "side":
					m.side.isVisible = true
					item = transcriptItem{renderContent: func() transcriptContent {
						return m.sideAnswerContent(sideThreadEntry{answer: markdown, thinking: "thinking", complete: true}, false)
					}}
				}
				item.gap = 2
				m.viewport.setItems([]transcriptItem{item})
				m.viewport.GotoTop()
				mouse := codeButtonMouse(t, m, source)
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				updated, command := m.Update(tea.MouseReleaseMsg(mouse))
				assertClipboard(t, command, source)
				if !updated.(model).copyNotice {
					t.Fatal("copy confirmation missing")
				}
			})
		}
	}
}

func TestCodeButtonRejectsStalePressAndDrag(t *testing.T) {
	for _, change := range []string{"source", "resize", "scroll", "drag"} {
		t.Run(change, func(t *testing.T) {
			m := codeTestModel(t, 60)
			m.entries = []transcriptEntry{{kind: entryAssistant, text: "```\noriginal\n```", complete: true}}
			m.refreshViewport(true)
			mouse := codeButtonMouse(t, m, "original\n")
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			switch change {
			case "source":
				m.entries[0].text = "```\nreplaced\n```"
				m.refreshViewport(false)
			case "resize":
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 45, Height: 40})
			case "scroll":
				m = updateModel(t, m, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
			case "drag":
				motion := mouse
				motion.X++
				m = updateModel(t, m, tea.MouseMotionMsg(motion))
			}
			_, command := m.Update(tea.MouseReleaseMsg(mouse))
			if change == "drag" {
				// A drag still selects display text, never the original block.
				if command != nil && fmt.Sprint(command().(tea.BatchMsg)[0]()) == "original\n" {
					t.Fatal("drag triggered whole-block copy")
				}
			} else if command != nil {
				t.Fatal("stale press triggered copy")
			}
		})
	}
}

func TestCodeGeometrySurvivesOuterWrappingAndBlankRows(t *testing.T) {
	content := transcriptContent{view: "\n" + strings.Repeat("long prefix ", 20) + "\n"}
	content.append(newCodeBlock("first\n", "text").layout(codeBlockOptions{width: 24}).content(), "\n")
	content.appendText("\n")
	content.append(newCodeBlock("second\n", "text").layout(codeBlockOptions{width: 24}).content(), "\n")
	content = content.wrapText(24).pad(3, 1)
	lines, codes := wrapTranscriptContent(content, 28)
	for _, block := range content.blocks {
		x := block.column + block.layout.copyColumn
		if !strings.HasPrefix(ansi.Strip(content.view), "\n") {
			// Padding makes the leading blank row spaces.
			if strings.TrimSpace(ansi.Strip(strings.Split(content.view, "\n")[0])) != "" {
				t.Fatal("leading empty row lost")
			}
		}
		if ansi.Strip(ansi.Cut(lines[block.row], x, x+6)) != "[Copy]" || codes[block.row].placement == nil {
			t.Fatal("code target detached from painted button")
		}
	}
}
