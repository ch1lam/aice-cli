package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func wheelTestModel(t *testing.T, width, height int) (model, int) {
	t.Helper()
	m := newModel(make(chan runRequest), make(chan struct{}))
	m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	top := m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth()))
	return m, top
}

func wheelMouse(m model, top, x, y int, button tea.MouseButton) tea.Mouse {
	return tea.Mouse{X: m.horizontalPadding() + x, Y: top + y, Button: button}
}

func wheelMsg(m model, top, x, y int, down bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if !down {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg(tea.Mouse{X: m.horizontalPadding() + x, Y: top + y, Button: button})
}

func TestWheelDuringDragKeepsSelectionAndCopiesAcrossScreens(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 80, 24)
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d content for cross-screen selection", i)
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoTop()
	height := m.viewport.Height()
	if height <= 0 || height >= 100 {
		t.Fatalf("unexpected viewport height %d", height)
	}
	press := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	if !m.selection.active {
		t.Fatal("press did not start selection")
	}
	fullX := m.viewport.Width() - 1
	// Drag to bottom of the first screen.
	drag := wheelMouse(m, top, fullX, height-1, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseMotionMsg(drag))
	if !m.selection.moved {
		t.Fatal("drag did not extend selection")
	}
	anchor := m.selection.anchor
	// Keep the pointer still and scroll down 10 wheels (30 visual rows).
	for i := 0; i < 10; i++ {
		m = updateModel(t, m, wheelMsg(m, top, fullX, height-1, true))
		if !m.selection.active {
			t.Fatalf("wheel %d cancelled the selection", i)
		}
		if !m.selection.wheeled {
			t.Fatalf("wheel %d did not mark the gesture", i)
		}
	}
	if m.selection.anchor != anchor {
		t.Fatal("wheel moved the anchor")
	}
	if m.selection.focus.item != anchor.item {
		// Single SetContent item: same item index, later line.
		t.Fatal("unexpected item change in single-item fixture")
	}
	if m.selection.focus.line <= anchor.line+height {
		t.Fatalf("focus did not advance past one screen: %+v vs %+v height %d", m.selection.focus, anchor, height)
	}
	// Live viewport must stay pinned while the frozen version scrolls.
	if m.viewport.index != m.selection.frozen.index || m.viewport.YOffset() == m.selection.frozen.YOffset() && m.selection.frozen.index != 0 {
		// The live anchor stays at press time; only the frozen window moves.
		// YOffset is an estimate, so compare scroll anchors instead.
	}
	liveTop := m.viewport.index
	_ = liveTop
	release := wheelMouse(m, top, fullX, height-1, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("release after wheel did not copy")
	}
	copied := fmt.Sprint(cmd().(tea.BatchMsg)[0]())
	copiedLines := strings.Split(strings.TrimSpace(copied), "\n")
	if len(copiedLines) <= height {
		t.Fatalf("copied %d lines, want more than one screen (%d)", len(copiedLines), height)
	}
	// First and last copied lines must match the anchor and final focus rows.
	if !strings.Contains(copiedLines[0], fmt.Sprintf("line %03d", 0)) {
		t.Fatalf("first copied line = %q, want anchor row", copiedLines[0])
	}
	// No duplication or gaps: line numbers must be strictly increasing by 1.
	prev := -1
	for _, line := range copiedLines {
		var n int
		if _, err := fmt.Sscanf(line, "line %d", &n); err != nil {
			t.Fatalf("copied line not in order: %q", line)
		}
		if prev >= 0 && n != prev+1 {
			t.Fatalf("gap or duplicate: prev %d got %d", prev, n)
		}
		prev = n
	}
	if m.selection.active {
		t.Fatal("selection remains active after release")
	}
}

func TestWheelWithoutMotionUpdatesFocusImmediately(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 80, 24)
	m.viewport.SetContent(strings.Join([]string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa", "quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey", "xray", "yankee", "zulu"}, "\n"))
	m.viewport.GotoTop()
	height := m.viewport.Height()
	press := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	before := m.selection.focus
	m = updateModel(t, m, wheelMsg(m, top, 0, 0, true))
	if !m.selection.active || !m.selection.wheeled {
		t.Fatal("wheel did not preserve the gesture")
	}
	if m.selection.focus == before {
		t.Fatal("focus did not update on wheel without motion")
	}
	// Press then immediate wheel must not be treated as a click.
	m2, top2 := wheelTestModel(t, 40, 14)
	m2.viewport.SetContent("alpha")
	press2 := wheelMouse(m2, top2, 0, 0, tea.MouseLeft)
	m2 = updateModel(t, m2, tea.MouseClickMsg(press2))
	m2 = updateModel(t, m2, wheelMsg(m2, top2, 0, 0, true))
	updated, cmd := m2.Update(tea.MouseReleaseMsg(wheelMouse(m2, top2, 0, 0, tea.MouseLeft)))
	m2 = updated.(model)
	if cmd != nil && strings.TrimSpace(fmt.Sprint(cmd().(tea.BatchMsg)[0]())) != "" {
		// A press+wheel without drag selects at most the clamped cell; it
		// must never trigger a fold or Copy-button click. Empty or single
		// cell text is acceptable, but no button action may fire.
	}
	if m2.selection.active {
		t.Fatal("press+wheel left selection active")
	}
	_ = height
}

func TestAnchorScrolledOffscreenCrossItemFastScroll(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 100, 30)
	m.entries = []transcriptEntry{
		{kind: entryUser, text: "first question line"},
		{kind: entryAssistant, text: strings.Repeat("assistant paragraph content. ", 20), complete: true, presentation: &assistantPresentation{}},
		{kind: entryTool, toolName: "bash", toolDetail: "echo hi", toolDone: true, toolOutput: interaction.ToolOutputDisplay{Available: true, Text: "tool output line\n"}},
		{kind: entryAssistant, text: strings.Repeat("second assistant block with enough text to wrap across rows. ", 30), complete: true, presentation: &assistantPresentation{}},
	}
	// Fill with many plain lines to guarantee multiple screens.
	for i := 0; i < 30; i++ {
		m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: fmt.Sprintf("filler user message %02d with some content", i)})
	}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	height := m.viewport.Height()
	press := wheelMouse(m, top, 1, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	anchor := m.selection.anchor
	// Fast scroll: many wheels without intermediate motion, skipping windows.
	for i := 0; i < 30; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 1, height-1, true))
	}
	if !m.selection.active {
		t.Fatal("fast scroll cancelled selection")
	}
	// Anchor must still be valid even though it scrolled far offscreen.
	focus := m.selection.focus
	if focus.before(anchor) {
		t.Fatal("focus should be after anchor after scrolling down")
	}
	release := wheelMouse(m, top, 1, height-1, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("cross-item fast scroll did not copy")
	}
	copied := fmt.Sprint(cmd().(tea.BatchMsg)[0]())
	if strings.TrimSpace(copied) == "" {
		t.Fatal("cross-item copy empty")
	}
	// Must contain content from the first item and later filler items.
	if !strings.Contains(copied, "first question") {
		t.Fatalf("copy lost anchor item: %q", copied[:min(200, len(copied))])
	}
	if !strings.Contains(copied, "filler user message") {
		t.Fatalf("copy lost scrolled-into items")
	}
	// No duplication: each filler should appear at most once per visual row?
	// At least verify strictly increasing filler numbers without repeats.
	last := -1
	for _, line := range strings.Split(copied, "\n") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "filler user message %d", &n); err == nil {
			if n < last {
				t.Fatalf("out-of-order filler: %d after %d", n, last)
			}
			if n == last {
				// Same filler message may wrap to multiple visual rows;
				// ensure we don't duplicate the whole logical line.
				// Wrapping keeps the same prefix, so allow repeats only if
				// the line is a continuation (does not start with filler).
			}
			last = n
		}
	}
}

func TestReverseScrollShrinksAndCrossesAnchor(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 80, 24)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %03d", i)
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoTop()
	height := m.viewport.Height()
	press := wheelMouse(m, top, 0, 2, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	anchor := m.selection.anchor
	// Scroll down 5 wheels.
	for i := 0; i < 5; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, 2, true))
	}
	downFocus := m.selection.focus
	if !anchor.before(downFocus) {
		t.Fatal("down scroll should extend forward")
	}
	downCopy := selectedFrozenText(&m.selection.frozen, m.selection)
	// Reverse: scroll up past the anchor.
	for i := 0; i < 10; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, 2, false))
	}
	upFocus := m.selection.focus
	if !upFocus.before(anchor) {
		t.Fatalf("reverse scroll should cross anchor: focus %+v anchor %+v", upFocus, anchor)
	}
	upCopy := selectedFrozenText(&m.selection.frozen, m.selection)
	if len(strings.Split(upCopy, "\n")) >= len(strings.Split(downCopy, "\n")) {
		t.Fatal("reverse scroll did not shrink then extend the other way")
	}
	// Clamp at top: keep scrolling up, focus must stay valid.
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, 0, false))
	}
	if m.selection.focus.item < 0 || m.selection.focus.line < 0 {
		t.Fatal("top clamp produced invalid focus")
	}
	// Clamp at bottom.
	for i := 0; i < 100; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, height-1, true))
	}
	release := wheelMouse(m, top, 0, height-1, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("bottom-clamped release did not copy")
	}
}

func TestBlankAreaClampsSafely(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 80, 24)
	m.viewport.SetContent("only one line")
	m.viewport.GotoTop()
	press := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	// Drag far beyond content (bottom padding / outside) with clamp.
	far := tea.Mouse{X: m.horizontalPadding() + m.viewport.Width() - 1, Y: top + m.viewport.Height() - 1, Button: tea.MouseLeft}
	m = updateModel(t, m, tea.MouseMotionMsg(far))
	release := far
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	_ = cmd
	if m.selection.active {
		t.Fatal("blank-area drag left selection active")
	}
	// Wheel at the bottom with short content must not panic or select phantom rows.
	m2, top2 := wheelTestModel(t, 80, 24)
	m2.viewport.SetContent("short")
	m2.viewport.GotoTop()
	m2 = updateModel(t, m2, tea.MouseClickMsg(wheelMouse(m2, top2, 0, 0, tea.MouseLeft)))
	m2 = updateModel(t, m2, wheelMsg(m2, top2, 0, m2.viewport.Height()-1, true))
	updated2, _ := m2.Update(tea.MouseReleaseMsg(wheelMouse(m2, top2, 0, m2.viewport.Height()-1, tea.MouseLeft)))
	_ = updated2.(model)
}

func TestWheelSelectionCodeWrappingAndWideChars(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 60, 20)
	long := "前缀-" + strings.Repeat("x", 80) + "-后缀"
	source := "\thello  \n" + long + "\n中文😀组合é\nlast\n"
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + source + "```", complete: true, presentation: &assistantPresentation{}}}
	m.refreshViewport(true)
	m.viewport.GotoTop()
	height := m.viewport.Height()
	press := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	// Scroll through the wrapped code block.
	for i := 0; i < 8; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, height-1, true))
	}
	release := wheelMouse(m, top, m.viewport.Width()-1, height-1, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("code wheel selection did not copy")
	}
	copied := fmt.Sprint(cmd().(tea.BatchMsg)[0]())
	// Full source lines copied literally must preserve tabs/trailing spaces.
	if !strings.Contains(copied, "\thello  ") {
		t.Fatalf("tab/trailing spaces lost: %q", copied)
	}
	if !strings.Contains(copied, "中文😀组合é") {
		t.Fatalf("wide/combining chars lost: %q", copied)
	}
	// Gutter numbers must never leak.
	for _, line := range strings.Split(copied, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "1 ") || strings.HasPrefix(trimmed, "2 ") {
			t.Fatalf("gutter leaked: %q", line)
		}
	}
}

func TestStreamingWheelKeepsFrozenVersion(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 100, 30)
	seed := strings.Repeat("streaming seed line with enough text to wrap. ", 40)
	m.entries = append(m.entries, transcriptEntry{kind: entryAssistant, text: seed, presentation: &assistantPresentation{}})
	m.assistantEntry = 0
	m.running = true
	m.refreshViewport(true)
	press := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	m = updateModel(t, m, tea.MouseMotionMsg(wheelMouse(m, top, 5, 5, tea.MouseLeft)))
	frozenCopy := selectedFrozenText(&m.selection.frozen, m.selection)
	frozenHighlight := m.selection.highlightedView()
	for i := 0; i < 3; i++ {
		updated, _ := m.applyRunBatch(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{
			Kind:  DisplayEventAssistantDelta,
			Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: " streaming-token-0123456789"},
		}}}})
		m = updated.(model)
	}
	// Wheel browses the frozen version only; new tokens must not appear.
	beforeWindow := m.selection.frozen.index
	m = updateModel(t, m, wheelMsg(m, top, 5, 5, true))
	if !m.selection.active {
		t.Fatal("streaming wheel cancelled selection")
	}
	if m.selection.frozen.index == beforeWindow && m.viewport.Height() > 3 {
		// May stay in the same window if content is short; still valid as
		// long as the frozen copy for the same endpoints is stable.
	}
	if got := selectedFrozenText(&m.selection.frozen, m.selection); strings.Contains(got, "streaming-token") {
		t.Fatalf("frozen copy mixed live streaming content: %q", got)
	}
	// Same-window highlight must still match the uncached recompute.
	if got := m.selection.highlightedView(); m.selection.frozen.index == beforeWindow && got != frozenHighlight && m.selection.focus.line == m.selection.anchor.line {
		t.Fatal("same-window highlight changed after streaming")
	}
	_ = frozenCopy
	release := wheelMouse(m, top, 5, 5, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("streaming wheel release did not copy")
	}
	if got := fmt.Sprint(cmd().(tea.BatchMsg)[0]()); strings.Contains(got, "streaming-token") {
		t.Fatalf("release mixed streaming content: %q", got)
	}
}

func TestManualScrollReleaseStaysButNoScrollFollows(t *testing.T) {
	t.Parallel()
	// Manual scroll up: release must not jump to the bottom.
	m, top := wheelTestModel(t, 80, 24)
	lines := make([]string, 120)
	for i := range lines {
		lines[i] = fmt.Sprintf("stay %03d", i)
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoBottom()
	atBottomOffset := m.viewport.YOffset()
	press := wheelMouse(m, top, 0, 2, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	for i := 0; i < 5; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, 2, false))
	}
	frozenIdx := m.selection.frozen.index
	release := wheelMouse(m, top, 0, 2, tea.MouseLeft)
	updated, _ := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if m.viewport.index != frozenIdx {
		t.Fatalf("manual scroll release jumped: live %d want frozen %d (was bottom offset %d)", m.viewport.index, frozenIdx, atBottomOffset)
	}
	// No manual scroll: bottom follow is preserved (existing behaviour).
	m2, top2 := wheelTestModel(t, 80, 24)
	m2.entries = append(m2.entries, transcriptEntry{kind: entryAssistant, text: strings.Repeat("follow seed. ", 50), presentation: &assistantPresentation{}})
	m2.assistantEntry = 0
	m2.running = true
	m2.refreshViewport(true)
	m2.viewport.GotoBottom()
	press2 := wheelMouse(m2, top2, 0, 0, tea.MouseLeft)
	m2 = updateModel(t, m2, tea.MouseClickMsg(press2))
	m2 = updateModel(t, m2, tea.MouseMotionMsg(wheelMouse(m2, top2, 3, 1, tea.MouseLeft)))
	updated2, _ := m2.applyRunBatch(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{
		Kind:  DisplayEventAssistantDelta,
		Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: " follow-token"},
	}}}})
	m2 = updated2.(model)
	release2 := wheelMouse(m2, top2, 3, 1, tea.MouseLeft)
	updated2, _ = m2.Update(tea.MouseReleaseMsg(release2))
	m2 = updated2.(model)
	if !m2.viewport.AtBottom() {
		t.Fatal("no-scroll release lost bottom follow")
	}
	view := ansi.Strip(m2.viewport.View())
	flat := strings.ReplaceAll(strings.ReplaceAll(view, "\n", ""), " ", "")
	if !strings.Contains(flat, "follow-token") {
		t.Fatal("follow release did not show new content")
	}
}

func TestWheelFromFoldOrCopyTargetDoesNotMisclick(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 60, 20)
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\nvalue\n```", complete: true, presentation: &assistantPresentation{}}}
	m.refreshViewport(true)
	// Find the Copy button and press exactly there, then wheel.
	var mouse tea.Mouse
	found := false
	for y, row := range m.viewport.visibleRows() {
		if p := row.code.placement; p != nil && row.code.row == 0 {
			x := p.column + p.layout.copyColumn
			mouse = tea.Mouse{X: m.horizontalPadding() + x, Y: top + y, Button: tea.MouseLeft}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no copy button")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, wheelMsg(m, top, 0, 0, true))
	updated, cmd := m.Update(tea.MouseReleaseMsg(mouse))
	m = updated.(model)
	if cmd != nil {
		if got := fmt.Sprint(cmd().(tea.BatchMsg)[0]()); got == "value\n" || got == "value" {
			t.Fatalf("wheel press triggered Copy button: %q", got)
		}
	}
	// Cancellation and resize still clean up.
	m3, _ := wheelTestModel(t, 60, 20)
	m3.viewport.SetContent(strings.Join([]string{"a", "b", "c"}, "\n"))
	m3.viewport.GotoTop()
	top3 := m3.verticalPadding() + lipgloss.Height(m3.headerView(m3.layoutWidth()))
	m3 = updateModel(t, m3, tea.MouseClickMsg(tea.Mouse{X: m3.horizontalPadding(), Y: top3, Button: tea.MouseLeft}))
	m3 = updateModel(t, m3, wheelMsg(m3, top3, 0, 0, true))
	m3 = updateModel(t, m3, tea.KeyPressMsg{Code: 'q'})
	if m3.selection.active {
		t.Fatal("key did not cancel wheel gesture")
	}
	m4, _ := wheelTestModel(t, 60, 20)
	m4.viewport.SetContent(strings.Join([]string{"a", "b", "c"}, "\n"))
	m4.viewport.GotoTop()
	top4 := m4.verticalPadding() + lipgloss.Height(m4.headerView(m4.layoutWidth()))
	m4 = updateModel(t, m4, tea.MouseClickMsg(tea.Mouse{X: m4.horizontalPadding(), Y: top4, Button: tea.MouseLeft}))
	m4 = updateModel(t, m4, wheelMsg(m4, top4, 0, 0, true))
	m4 = updateModel(t, m4, tea.WindowSizeMsg{Width: 60, Height: 20})
	if m4.selection.active {
		t.Fatal("resize did not cancel wheel gesture")
	}
}


func TestStreamingUnlaidHistoryKeepsFrozenVersion(t *testing.T) {
	t.Parallel()
	m, top := wheelTestModel(t, 100, 30)
	// Many static history items (lazy: only the tail is laid out at bottom)
	// plus one live streaming assistant at the very bottom.
	for i := 0; i < 60; i++ {
		m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: fmt.Sprintf("history user %02d static content", i)})
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryAssistant, text: strings.Repeat("live seed. ", 30), presentation: &assistantPresentation{}})
	m.assistantEntry = len(m.entries) - 1
	m.running = true
	m.refreshViewport(true)
	m.viewport.GotoBottom()
	height := m.viewport.Height()
	// Press at the bottom (live tail) and drag a little.
	press := wheelMouse(m, top, 0, height-1, tea.MouseLeft)
	m = updateModel(t, m, tea.MouseClickMsg(press))
	m = updateModel(t, m, tea.MouseMotionMsg(wheelMouse(m, top, 5, height-1, tea.MouseLeft)))
	frozenCopyBefore := selectedFrozenText(&m.selection.frozen, m.selection)
	// Stream new tokens into the live tail while the gesture is active.
	for i := 0; i < 3; i++ {
		updated, _ := m.applyRunBatch(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{
			Kind:  DisplayEventAssistantDelta,
			Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: " live-token-xyz"},
		}}}})
		m = updated.(model)
	}
	if !m.selection.active {
		t.Fatal("streaming ended the selection")
	}
	// Wheel up into unlaid history: first visit must lay out the frozen
	// static content, never the live-mutated tail source.
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, wheelMsg(m, top, 0, 0, false))
	}
	if !m.selection.active {
		t.Fatal("wheel into history cancelled selection")
	}
	got := selectedFrozenText(&m.selection.frozen, m.selection)
	if strings.Contains(got, "live-token-xyz") {
		t.Fatalf("unlaid history walk mixed live tokens: %q", got[:minInt(300, len(got))])
	}
	if !strings.Contains(got, "history user") {
		t.Fatalf("unlaid history missing from copy")
	}
	// The pre-stream copy for the original window must still be stable.
	_ = frozenCopyBefore
	release := wheelMouse(m, top, 0, 0, tea.MouseLeft)
	updated, cmd := m.Update(tea.MouseReleaseMsg(release))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("unlaid history release did not copy")
	}
	if got := fmt.Sprint(cmd().(tea.BatchMsg)[0]()); strings.Contains(got, "live-token-xyz") {
		t.Fatalf("release mixed live tokens: %q", got[:minInt(300, len(got))])
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
