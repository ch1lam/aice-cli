package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestFoldHeadingsReuseReachedRowsAndLazyHover(t *testing.T) {
	m := newModel(nil, nil)
	m.width, m.height = 100, 32
	m.resizeLayout()
	id := m.beginProcess()
	for range 100 {
		m.entries = append(m.entries,
			transcriptEntry{kind: entryAssistant, processID: id, thinking: "reasoning", complete: true},
			transcriptEntry{kind: entryTool, processID: id, toolName: "read", toolDetail: "file.go", toolDone: true},
		)
	}
	calls, hovers := map[int]int{}, map[int]int{}
	build := func() []transcriptItem {
		items := m.transcriptItems()
		for i := range items {
			item := &items[i]
			if item.fold.kind == foldNone {
				continue
			}
			key, render, hover := item.key, item.render, item.renderHover
			item.render = func() string { calls[key]++; return render() }
			if hover != nil {
				item.renderHover = func() string { hovers[key]++; return hover() }
			}
		}
		return items
	}
	m.viewport = newTranscriptViewport()
	m.viewport.SetHeight(8)
	m.viewport.setItems(build())
	m.viewport.GotoBottom()
	for range 3 {
		m.viewport.View()
		m.viewport.setItems(build())
	}
	if calls[5] != 0 || calls[24] != 0 || calls[25] != 0 {
		t.Fatal("unreached historical headings were rendered")
	}
	if len(hovers) != 0 {
		t.Fatal("unhovered headings rendered hover styles")
	}
	target := foldTarget{kind: foldTool, id: len(m.entries) - 1}
	key := target.id*16 + 9
	for range 3 {
		m.viewport.viewWithHover(target)
		m.viewport.setItems(build())
	}
	if calls[key] != 1 || hovers[key] != 1 {
		t.Fatalf("unchanged visible heading rendered normal=%d hover=%d times", calls[key], hovers[key])
	}
	m.viewport.GotoTop()
	m.viewport.View()
	if calls[5] != 1 {
		t.Fatal("scrolling did not materialize the older heading")
	}
	m.viewport.GotoBottom()
	m.viewport.viewWithHover(target)
	if calls[key] != 1 || hovers[key] != 1 {
		t.Fatal("scrolling discarded cached title")
	}
	m.entries[target.id].toolDetail = "changed.go"
	m.viewport.setItems(build())
	m.viewport.GotoBottom()
	if !strings.Contains(ansi.Strip(m.viewport.viewWithHover(target)), "changed.go") || calls[key] != 2 || hovers[key] != 2 {
		t.Fatalf("changed title retained normal or hover cache: normal=%d hover=%d view=%q", calls[key], hovers[key], m.viewport.viewWithHover(target))
	}
	m.width = 40
	m.resizeLayout()
	m.viewport.setItems(build())
	m.viewport.GotoBottom()
	m.viewport.viewWithHover(target)
	if calls[key] != 3 || hovers[key] != 3 {
		t.Fatal("resize retained stale wrapped headings")
	}
}

func TestFoldCachedHeadingsMatchFreshRenderingAfterUpdates(t *testing.T) {
	m := foldTestModel()
	check := func() string {
		t.Helper()
		m.refreshViewport(false)
		got := m.viewport.GetContent()
		want := strings.Join(wrapTranscriptLines(m.transcriptView(), m.viewport.Width()), "\n")
		if got != want {
			t.Fatalf("cached transcript differs from fresh rendering:\n%s\nwant:\n%s", got, want)
		}
		return got
	}
	check()
	m.entries[2].toolDone = false
	m.spinner.Spinner = spinner.Spinner{Frames: []string{"SPIN_A", "SPIN_B"}}
	before := check()
	m.spinner, _ = m.spinner.Update(spinner.TickMsg{})
	if after := check(); before == after {
		t.Fatal("running tool spinner did not advance")
	}
	m.completeTool(ToolDisplay{ID: "read-1", Failed: true,
		Output: interaction.ToolOutputDisplay{Available: true, Text: "failure\nsecond line"}})
	check()
	m.entries[1].toolName = "read"
	if !strings.Contains(check(), "2 file reads") {
		t.Fatal("tool group counts stayed stale")
	}
	m.entries[1].toolName, m.entries[2].toolName = "bash", "read"
	check()
	m.entries[1].toolName, m.entries[2].toolName = "read", "bash"
	check()
	m.setFoldExpanded(foldTarget{kind: foldCalls, id: 1}, false)
	check()
	m.setFoldExpanded(foldTarget{kind: foldCalls, id: 1}, true)
	check()
	m.entries[2] = transcriptEntry{kind: entryTool, processID: 1, toolName: "write", toolPreparing: true, writePreview: &writePreview{}}
	m.entries[2].writePreview.setContent(ToolDisplay{Content: "one\n", HasContent: true})
	m.entries[2].toolPreviewRevision = m.entries[2].writePreview.revision
	check()
	m.entries[2].writePreview.setContent(ToolDisplay{Content: "one\ntwo\n", HasContent: true})
	m.entries[2].toolPreviewRevision = m.entries[2].writePreview.revision
	if !strings.Contains(check(), "2 lines") {
		t.Fatal("preview line count stayed stale")
	}
	m.setFoldExpanded(foldTarget{kind: foldTool, id: 2}, true)
	check()
	m.entries[0].complete, m.running, m.assistantEntry = false, true, 0
	m.entries[0].text = ""
	m.status = "FIRST_STATUS"
	check()
	m.status = "SECOND_STATUS"
	if !strings.Contains(check(), "SECOND_STATUS") {
		t.Fatal("thinking activity stayed stale")
	}
	m.width = 40
	m.resizeLayout()
	check()
	m.resetBranchTranscript()
	check()
}
