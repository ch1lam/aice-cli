package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTranscriptViewportRendersOnlyReachedItems(t *testing.T) {
	calls := make([]int, 1000)
	items := make([]transcriptItem, len(calls))
	for i := range items {
		items[i] = transcriptItem{key: i, version: i, render: func() string { calls[i]++; return fmt.Sprintf("item %d\nsecond row", i) }}
	}
	v := newTranscriptViewport()
	v.SetHeight(4)
	v.setItems(items)
	v.GotoBottom()
	for range 10 {
		v.View()
		v.AtBottom()
		v.YOffset()
	}
	for i, n := range calls {
		if i < 998 && n != 0 {
			t.Fatalf("offscreen item %d rendered %d times", i, n)
		}
		if n > 1 {
			t.Fatalf("unchanged item %d rendered %d times", i, n)
		}
	}
	if !strings.Contains(v.View(), "item 999") {
		t.Fatal("tail missing")
	}
	v.PageUp()
	if !strings.Contains(v.View(), "item 996") {
		t.Fatal("scroll did not load older content")
	}
	v.GotoBottom()
	v.View()
	if calls[999] != 1 {
		t.Fatal("scroll discarded cached completed content")
	}
}

func TestTranscriptViewportScrollMatchesRows(t *testing.T) {
	v := newTranscriptViewport()
	v.SetWidth(12)
	v.SetHeight(3)
	items := []transcriptItem{
		staticTranscriptItem(0, "alpha\nbeta"),
		staticTranscriptItem(1, "中文\n🙂 end"),
		staticTranscriptItem(2, "last\nrow\nfinish"),
	}
	items[1].gap = 1
	v.setItems(items)
	all := strings.Split(v.GetContent(), "\n")
	offset := 0
	for _, delta := range []int{1, 2, 1, 3, -1, -4, -20, 100, -2, 1} {
		v.scroll(delta)
		offset = min(max(offset+delta, 0), len(all)-v.Height())
		got := strings.Split(ansi.Strip(v.View()), "\n")
		for i, want := range all[offset : offset+v.Height()] {
			if strings.TrimRight(got[i], " ") != want {
				t.Fatalf("delta %d row %d: %q, want %q", delta, i, got[i], want)
			}
		}
		if v.AtBottom() != (offset == len(all)-v.Height()) {
			t.Fatal("incorrect bottom state")
		}
	}
}

func TestTranscriptViewportPreservesAnchorAndInvalidatesChangedContent(t *testing.T) {
	v := newTranscriptViewport()
	v.SetWidth(20)
	v.SetHeight(2)
	v.setItems([]transcriptItem{staticTranscriptItem(1, "first"), staticTranscriptItem(2, "anchor\nsecond"), staticTranscriptItem(3, "tail")})
	v.scroll(1)
	v.setItems([]transcriptItem{staticTranscriptItem(0, "inserted"), staticTranscriptItem(1, "first"), staticTranscriptItem(2, "anchor\nsecond"), staticTranscriptItem(3, "changed tail")})
	if !strings.HasPrefix(v.View(), "anchor") {
		t.Fatal("insertion moved the reading anchor")
	}
	v.GotoBottom()
	if !strings.Contains(v.View(), "changed tail") {
		t.Fatal("changed content retained stale cache")
	}
	v.SetWidth(7)
	v.setItems([]transcriptItem{staticTranscriptItem(3, "中文中文中文END")})
	v.GotoBottom()
	if !strings.Contains(strings.ReplaceAll(v.GetContent(), "\n", ""), "END") {
		t.Fatal("resize lost wide text")
	}
	for _, line := range strings.Split(v.GetContent(), "\n") {
		if ansi.StringWidth(line) > 7 {
			t.Fatal("resize failed to wrap")
		}
	}
}

func TestLongTranscriptLeavesUnseenThinkingUnrendered(t *testing.T) {
	m := newModel(nil, nil)
	m.width, m.height, m.running = 80, 24, true
	m.resizeLayout()
	id := m.beginProcess()
	for i := range 1000 {
		m.entries = append(m.entries, transcriptEntry{kind: entryAssistant, processID: id, complete: true,
			thinking: fmt.Sprintf("block %d ", i) + strings.Repeat("reasoning ", 100), presentation: &assistantPresentation{}})
	}
	m.expandAllDetails(true)
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	m.refreshViewport(true)
	for range 3 {
		m.applyAssistantDelta(DisplayEvent{Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: "latest "}})
		m.refreshViewport(true)
		m.View()
	}
	for i := 1; i < 990; i++ {
		if m.entries[i].presentation.thinkingCache.rendered != "" {
			t.Fatalf("rendered unseen thinking %d", i)
		}
	}
	m.viewport.GotoTop()
	m.View()
	if m.entries[0].presentation.thinkingCache.rendered == "" {
		t.Fatal("scrolling to the beginning did not render full history")
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 50, Height: 20})
	if !strings.Contains(ansi.Strip(m.viewport.View()), "block 0") {
		t.Fatal("resize moved the historical reading position")
	}
	m.toggleProcessGroups()
	m.refreshViewport(true)
	if strings.Contains(ansi.Strip(m.viewport.View()), "block 0") {
		t.Fatal("collapse exposed reasoning")
	}
	m.toggleProcessGroups()
	m.refreshViewport(true)
	if !strings.Contains(ansi.Strip(m.viewport.View()), "latest") {
		t.Fatal("expand lost the current answer")
	}
}

func TestTranscriptWrappedRowsPreserveStylesAndWideCharacters(t *testing.T) {
	source := "\x1b[31m中文🙂abcdef\x1b[m"
	rows := wrapTranscriptLines(source, 5)
	if ansi.Strip(strings.Join(rows, "")) != ansi.Strip(source) {
		t.Fatal("wrapping lost text at a wide-character boundary")
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > 5 || !strings.Contains(row, "\x1b[31m") {
			t.Fatalf("row lost its style or exceeded width: %q", row)
		}
	}
}
