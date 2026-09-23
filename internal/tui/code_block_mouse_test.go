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

func codeLineMouse(t *testing.T, m model, sourceLine int, last bool) tea.Mouse {
	t.Helper()
	var found bool
	var mouse tea.Mouse
	for y, row := range m.viewport.visibleRows() {
		p := row.code.placement
		if p == nil || p.layout.rows[row.code.row].sourceLine != sourceLine {
			continue
		}
		found = true
		mouse = tea.Mouse{X: m.horizontalPadding() + p.column + p.layout.contentColumn,
			Y: m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth())) + y, Button: tea.MouseLeft}
		if !last {
			break
		}
	}
	if !found {
		t.Fatalf("source line %d not visible", sourceLine)
	}
	return mouse
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
			if change == "drag" || change == "scroll" {
				// A drag or a wheel-scroll still selects display text, never
				// the original block. The wheel revokes the Copy-button
				// click but keeps the text range for a cross-screen drag.
				if command != nil && fmt.Sprint(command().(tea.BatchMsg)[0]()) == "original\n" {
					t.Fatal(change + " triggered whole-block copy")
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

func TestCodeLineClickCopiesLogicalSource(t *testing.T) {
	for _, clip := range []bool{false, true} {
		t.Run(fmt.Sprint(clip), func(t *testing.T) {
			m := codeTestModel(t, 40)
			lines := []string{"\t" + strings.Repeat("中文😀", 18) + "  ", "", "   ", "\\n\x1b[0m", "last\r"}
			source := strings.Join(lines, "\r\n")
			content := newCodeBlock(source, "text").layout(codeBlockOptions{width: 32, clip: clip}).content().pad(3, 1)
			m.viewport.setItems([]transcriptItem{{renderContent: func() transcriptContent { return content }}})
			m.viewport.GotoTop()
			for i, want := range lines {
				mouse := codeLineMouse(t, m, i, true)
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				updated, command := m.Update(tea.MouseReleaseMsg(mouse))
				m = updated.(model)
				assertClipboard(t, command, want)
			}
		})
	}
}

func TestCodeLineHoverPreservesGeometryAndHighlighting(t *testing.T) {
	m := codeTestModel(t, 40)
	source := "var value = \"" + strings.Repeat("wide ", 20) + "\"\nother\n"
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```go\n" + source + "```", complete: true}}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	before := m.View().Content
	plain := ansi.Strip(before)
	mouse := codeLineMouse(t, m, 0, true)
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	after := m.View().Content
	if before == after || ansi.Strip(after) != plain {
		t.Fatal("hover must change styling without changing text or geometry")
	}
	rows := m.viewport.visibleRows()
	viewRows := strings.Split(m.viewport.viewWithCodeHover(foldTarget{}, m.hoveredCode()), "\n")
	changed := 0
	for i, row := range rows {
		p := row.code.placement
		if p == nil || p.layout.rows[row.code.row].sourceLine != 0 {
			if viewRows[i] != strings.Split(m.viewport.View(), "\n")[i] {
				t.Fatal("hover changed unrelated source or prose")
			}
			continue
		}
		changed++
		start, end := p.column+2, p.column+p.layout.width-2
		assertCanvasBackground(t, ansi.Cut(viewRows[i], start, end), subtleColor)
		// Original syntax foreground sequences survive background repainting.
		var state byte
		text := row.text
		for len(text) > 0 {
			sequence, _, n, next := ansi.DecodeSequence(text, state, nil)
			text, state = text[n:], next
			if strings.HasPrefix(sequence, "\x1b[38;") && !strings.Contains(viewRows[i], sequence) {
				t.Fatal("hover removed syntax foreground")
			}
		}
	}
	if changed < 2 {
		t.Fatal("wrapped continuations did not share hover")
	}
	assertCanvasBackground(t, after, nil)
	m = updateModel(t, m, tea.MouseMotionMsg(tea.Mouse{X: 0, Y: 0}))
	if m.View().Content != before {
		t.Fatal("leaving the code row did not restore its appearance")
	}
	// Starting a selection suppresses hover and preserves its clean snapshot.
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	if m.hoveredCode().valid || m.selection.frozen.View() != m.viewport.View() {
		t.Fatal("hover leaked into the drag-selection snapshot")
	}
}

func TestCodeButtonHoverAndNonSourcePadding(t *testing.T) {
	m := codeTestModel(t, 60)
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\nvalue\n```", complete: true}}
	m.refreshViewport(true)
	mouse := codeButtonMouse(t, m, "value\n")
	before := m.View().Content
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	after := m.View().Content
	if before == after || ansi.Strip(before) != ansi.Strip(after) {
		t.Fatal("button hover changed content or had no visible effect")
	}
	for screenRow, row := range m.viewport.visibleRows() {
		p := row.code.placement
		if p == nil {
			continue
		}
		y := m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth())) + screenRow
		for _, x := range []int{p.column, p.column + 1, p.column + p.layout.width - 1} {
			if m.codeHitAt(tea.Mouse{X: m.horizontalPadding() + x, Y: y}).valid {
				t.Fatal("panel padding is copyable")
			}
		}
	}
}

func TestCodeLineClickAfterScrollAndResize(t *testing.T) {
	m := codeTestModel(t, 60)
	long := strings.Repeat("原始行 ", 100)
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + long + "\nlast\n```", complete: true}}
	m.refreshViewport(true)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 30, Height: 16})
	m.viewport.GotoBottom()
	mouse := codeLineMouse(t, m, 0, true)
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	_, command := m.Update(tea.MouseReleaseMsg(mouse))
	assertClipboard(t, command, long)
}

func TestCodeHoverDistinguishesIdenticalBlocks(t *testing.T) {
	m := codeTestModel(t, 60)
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```\nsame\n```\n\n```\nsame\n```", complete: true}}
	m.refreshViewport(true)
	before := strings.Split(m.viewport.View(), "\n")
	mouse := codeLineMouse(t, m, 0, true)
	m = updateModel(t, m, tea.MouseMotionMsg(mouse))
	rows := m.viewport.visibleRows()
	after := strings.Split(m.viewport.viewWithCodeHover(foldTarget{}, m.hoveredCode()), "\n")
	changed := 0
	for i, row := range rows {
		if before[i] != after[i] {
			changed++
			if row.code.block != 1 || row.code.placement == nil {
				t.Fatal("hover matched source text instead of block identity")
			}
		}
	}
	if changed != 1 {
		t.Fatalf("changed %d rows, want only the second block's source row", changed)
	}
}

func codeDragSelection(frozen *transcriptViewport, rows []transcriptRow, startRow, startColumn, endRow, endColumn int) transcriptSelection {
	if len(rows) == 0 || frozen == nil {
		return transcriptSelection{}
	}
	start := rows[startRow]
	end := rows[endRow]
	sel := transcriptSelection{
		anchor: selectionPoint{item: start.item, part: start.part, line: start.line, column: startColumn},
		focus:  selectionPoint{item: end.item, part: end.part, line: end.line, column: endColumn},
		frozen: *frozen,
		active: true,
		moved:  true,
	}
	return sel
}

func codeSourceRows(rows []transcriptRow, source string) (int, int) {
	start, end := -1, -1
	for i, row := range rows {
		if p := row.code.placement; p != nil && p.layout.block.source == source {
			if l := p.layout.rows[row.code.row].sourceLine; l >= 0 {
				if start < 0 {
					start = i
				}
				end = i
			}
		}
	}
	return start, end
}

func TestCodeDragCopiesSourceWithoutLineNumbers(t *testing.T) {
	m := codeTestModel(t, 60)
	source := "first\nsecond\nthird\n"
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + source + "```", complete: true}}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	rows := m.viewport.visibleRows()
	start, end := codeSourceRows(rows, source)
	if start < 0 {
		t.Fatal("no code source rows")
	}
	// Drag from the gutter across the full width: numbers must not leak.
	selection := codeDragSelection(&m.viewport, rows, start, 0, end, 1000)
	if got, want := selectedFrozenText(&selection.frozen, selection), strings.TrimSuffix(source, "\n"); got != want {
		t.Fatalf("drag copy = %q, want %q", got, want)
	}
	for _, line := range strings.Split(selectedFrozenText(&selection.frozen, selection), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "1 ") || strings.HasPrefix(trimmed, "2 ") || strings.HasPrefix(trimmed, "3 ") {
			t.Fatalf("gutter leaked into %q", line)
		}
	}
	// Gutter-only on one visual row copies nothing.
	gutter := codeDragSelection(&m.viewport, rows, start, 0, start, 1)
	if got := selectedFrozenText(&gutter.frozen, gutter); strings.TrimSpace(got) != "" {
		t.Fatalf("gutter-only drag copied %q", got)
	}
}

func TestCodeDragPreservesLiteralAndWrappedLines(t *testing.T) {
	m := codeTestModel(t, 40)
	source := "\thello  \nsecond line\n"
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + source + "```", complete: true}}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	rows := m.viewport.visibleRows()
	var start, end = -1, -1
	for i, row := range rows {
		if p := row.code.placement; p != nil && p.layout.block.source == source && p.layout.rows[row.code.row].sourceLine == 0 {
			if start < 0 {
				start = i
			}
			end = i
		}
	}
	if start < 0 {
		t.Fatal("no rows for source line 0")
	}
	selection := codeDragSelection(&m.viewport, rows, start, 0, end, 1000)
	if got := selectedFrozenText(&selection.frozen, selection); got != "\thello  " {
		t.Fatalf("tab drag = %q, want literal with trailing spaces", got)
	}

	wide := codeTestModel(t, 30)
	long := "prefix-" + strings.Repeat("x", 100) + "-suffix"
	wideSource := long + "\nnext\n"
	wide.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + wideSource + "```", complete: true}}
	wide.refreshViewport(true)
	wide.viewport.GotoTop()
	wideRows := wide.viewport.visibleRows()
	wideStart, wideEnd := codeSourceRows(wideRows, wideSource)
	if wideStart < 0 {
		t.Fatal("no wrapped code rows")
	}
	wideSelection := codeDragSelection(&wide.viewport, wideRows, wideStart, 0, wideEnd, 1000)
	if got, want := selectedFrozenText(&wideSelection.frozen, wideSelection), strings.TrimSuffix(wideSource, "\n"); got != want {
		t.Fatalf("wrapped drag = %q, want %q", got, want)
	}
}
