package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func historyCodeModel(t *testing.T, source string) model {
	t.Helper()
	m := codeTestModel(t, 80)
	m.entries = []transcriptEntry{{kind: entryAssistant, complete: true, text: "```text\n" + source + "```\n\nReadable conclusion.", presentation: &assistantPresentation{}}}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	return m
}

func TestHistoryCodePreviewExpansionAndFullCopy(t *testing.T) {
	source := strings.Repeat("literal log\r\n", 180)
	m := historyCodeModel(t, source)
	if !strings.Contains(ansi.Strip(m.viewport.View()), "preview") || !strings.Contains(ansi.Strip(m.viewport.View()), "Readable conclusion") {
		t.Fatal("code hides conclusion or lacks preview notice")
	}
	mouse := codeButtonMouse(t, m, source)
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	updated, command := m.Update(tea.MouseReleaseMsg(mouse))
	assertClipboard(t, command, source)
	m = updated.(model)
	for _, row := range m.viewport.visibleRows() {
		if row.code.placement != nil && row.code.row == 0 {
			mouse.X = m.horizontalPadding() + row.code.placement.column + 2
			break
		}
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	updated, command = m.Update(tea.MouseReleaseMsg(mouse))
	if command != nil {
		t.Fatal("expansion attempted clipboard write")
	}
	m = updated.(model)
	if strings.Contains(ansi.Strip(m.viewport.View()), "preview") || !strings.Contains(ansi.Strip(m.viewport.View()), "▾") {
		t.Fatal("click did not expand code")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt})
	if !strings.Contains(ansi.Strip(m.viewport.View()), "preview") {
		t.Fatal("Alt+O did not collapse code")
	}
}

func TestHistoryCodeSearchRevealsLiteralLine(t *testing.T) {
	source := strings.Repeat("old log\n", 170) + "needle-at-end\n"
	m := historyCodeModel(t, source)
	m.jumpReadingEntry(0, "needle-at-end")
	if !strings.Contains(ansi.Strip(m.viewport.View()), "needle-at-end") {
		t.Fatal("search did not expand and locate hidden line")
	}
	m.viewport.GotoBottom()
	if !strings.Contains(ansi.Strip(m.viewport.View()), "Readable conclusion") {
		t.Fatal("latest conclusion missing")
	}
}

func TestHistoryCodeSingleLongLinePreview(t *testing.T) {
	source := strings.Repeat("literal ", 8192) + "tail\n"
	m := historyCodeModel(t, source)
	if len(m.viewport.GetContent()) > 10000 {
		t.Fatal("collapsed single line was fully laid out")
	}
	mouse := codeButtonMouse(t, m, source)
	if hit := m.codeHitAt(mouse); hit.text != source {
		t.Fatal("preview truncated copy source")
	}
}

func TestReadingCodeFoldsDoNotChangeLivePresentation(t *testing.T) {
	m := historyCodeModel(t, strings.Repeat("log\n", 180))
	original := m.entries[0].presentation
	reader := m.openCurrentReading()
	reader.reading.directory = false
	reader.viewport = reader.reading.savedViewport
	reader.viewport.GotoTop()
	reader.toggleVisibleCode()
	if reader.entries[0].presentation == original {
		t.Fatal("reader shares mutable presentation")
	}
	if *original.history.parts[0].blocks[0].expanded {
		t.Fatal("reading changed live fold")
	}
}

func TestHistoryMatchAcrossWrappedRows(t *testing.T) {
	lines := []string{"earlier", "  prefix unique   ", "  中文 phrase suffix "}
	if got := historyMatchRow(lines, nil, "unique 中文 phrase"); got != 1 {
		t.Fatalf("match row %d", got)
	}
}

func BenchmarkHistoryCodeFirstView(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			source := "```text\n" + strings.Repeat("Repeated log line.\n", count) + "```\n\nFinal conclusion."
			b.ReportAllocs()
			for b.Loop() {
				m := newModel(nil, nil)
				m.width, m.height = 120, 40
				m.resizeLayout()
				m.entries = []transcriptEntry{{kind: entryAssistant, complete: true, text: source, presentation: &assistantPresentation{}}}
				m.refreshViewport(true)
				m.View()
			}
		})
	}
}

func TestNestedHistoryCodePreviewKeepsLiteralCopy(t *testing.T) {
	source := strings.Repeat("nested log\r\n", 120)
	m := codeTestModel(t, 60)
	markdown := "> - item\n>\n>   ```text\n" + strings.Repeat(">   nested log\r\n", 120) + ">   ```\n\nConclusion."
	m.entries = []transcriptEntry{{kind: entryAssistant, complete: true, text: markdown, presentation: &assistantPresentation{}}}
	m.refreshViewport(true)
	mouse := codeButtonMouse(t, m, source)
	if hit := m.codeHitAt(mouse); hit.text != source {
		t.Fatal("nested preview lost literal source")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	_, command := m.Update(tea.MouseReleaseMsg(mouse))
	assertClipboard(t, command, source)
}
