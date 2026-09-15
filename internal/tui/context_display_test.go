package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestContextStatusUsedPercentage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		usage DisplayContext
		want  string
	}{
		{"small window", DisplayContext{Tokens: 50000, Window: 200000, Known: true}, "25.00%"},
		{"long window", DisplayContext{Tokens: 50000, Window: 1000000, Known: true}, "5.00%"},
		{"estimated", DisplayContext{Tokens: 50000, Window: 200000, Known: true, Estimated: true}, "25.00%"},
		{"full", DisplayContext{Tokens: 200000, Window: 200000, Known: true}, "100.00%"},
		{"negative", DisplayContext{Tokens: -1, Window: 200000, Known: true}, "0.00%"},
		{"overflow", DisplayContext{Tokens: 210000, Window: 200000, Known: true}, "100.00%"},
		{"empty", DisplayContext{Window: 200000, Known: true}, "0.00%"},
		{"unknown model", DisplayContext{Known: true}, "?%"},
		{"unknown usage", DisplayContext{Window: 200000}, "?%"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := newModel(nil, nil)
			current.contextUsage = test.usage
			if got := ansi.Strip(current.contextStatus()); got != test.want {
				t.Fatalf("%q, want %q", got, test.want)
			}
		})
	}
}

func TestContextStatusSurvivesNarrowTerminalsAndUpdates(t *testing.T) {
	t.Parallel()
	current := newModel(nil, nil)
	current.currentModel = DisplayModel{ID: "deepseek-v4-flash"}
	usage := DisplayContext{Tokens: 50000, Window: 200000, Known: true}
	current.applyAgentEvent(DisplayEvent{Context: &usage})
	for _, width := range []int{24, 32, 60, 80, 120, 160} {
		line := current.headerView(width)
		if !strings.Contains(ansi.Strip(line), "25.00%") {
			t.Fatalf("width %d: %q", width, line)
		}
		if lipgloss.Width(line) > width || lipgloss.Height(line) != 2 {
			t.Fatalf("wrapped at %d: %q", width, line)
		}
		if rows := strings.Split(ansi.Strip(line), "\n"); strings.TrimSpace(rows[1]) != "" {
			t.Fatalf("status bar has no blank row below it: %q", line)
		}
	}
	updated, _ := current.applyRunBatch(runBatchMsg{updates: []runUpdate{{state: &RuntimeState{
		Model: DisplayModel{ID: "other"}, Context: DisplayContext{Window: 1000000, Tokens: 50000, Known: true},
	}}}})
	current = updated.(model)
	if !strings.Contains(current.contextStatus(), "5.00%") {
		t.Fatal("model switch kept old percentage")
	}
}

func TestContextHeaderHoverAndClick(t *testing.T) {
	for _, width := range []int{24, 40, 80} {
		current := newModel(nil, nil)
		current.contextUsage = DisplayContext{Tokens: 168000, Window: 1000000, Known: true}
		current = updateModel(t, current, tea.WindowSizeMsg{Width: width, Height: 24})
		before := current.View()
		// Locate the percentage from painted cells, independently of hit testing.
		var mouse tea.Mouse
		for y, line := range strings.Split(ansi.Strip(before.Content), "\n") {
			if x := strings.Index(line, "16.80%"); x >= 0 {
				mouse = tea.Mouse{X: ansi.StringWidth(line[:x]), Y: y, Button: tea.MouseLeft}
			}
		}
		assertFraction := func(want bool) {
			t.Helper()
			view := current.View()
			if strings.Contains(ansi.Strip(view.Content), "168K / 1M") != want {
				t.Fatalf("fraction visibility should be %v:\n%s", want, ansi.Strip(view.Content))
			}
			if *before.Cursor != *view.Cursor || lipgloss.Height(view.Content) != 24 || lipgloss.Width(view.Content) > width {
				t.Fatal("context interaction changed screen bounds or cursor")
			}
			if strings.Contains(current.footerView(width), "16.80%") {
				t.Fatal("context still appears in the footer")
			}
		}
		current = updateModel(t, current, tea.MouseMotionMsg(mouse))
		assertFraction(true)
		current = updateModel(t, current, tea.MouseMotionMsg{X: 0, Y: 0})
		assertFraction(false)
		click := func(want bool) {
			current = updateModel(t, current, tea.MouseMotionMsg(mouse))
			assertFraction(want)
			for range 2 {
				current = updateModel(t, current, tea.MouseClickMsg(mouse))
				assertFraction(want)
				current = updateModel(t, current, tea.MouseReleaseMsg(mouse))
				assertFraction(want)
				current = updateModel(t, current, tea.MouseMotionMsg{X: mouse.X + 1, Y: mouse.Y})
				assertFraction(want)
			}
			current = updateModel(t, current, tea.MouseMotionMsg{X: 0, Y: 0})
			assertFraction(want)
		}
		click(true)
		current = updateModel(t, current, tea.MouseMotionMsg(mouse))
		assertFraction(false)
		current = updateModel(t, current, tea.MouseMotionMsg{X: 0, Y: 0})
		assertFraction(true)
		click(false)
		click(true)
		current = updateModel(t, current, tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		assertFraction(true)
		current = updateModel(t, current, tea.BlurMsg{})
		assertFraction(true)
		// A drag must not change the selected format.
		current = updateModel(t, current, tea.MouseClickMsg(mouse))
		current = updateModel(t, current, tea.MouseMotionMsg{X: mouse.X - 1, Y: mouse.Y, Button: tea.MouseLeft})
		current = updateModel(t, current, tea.MouseReleaseMsg(mouse))
		current = updateModel(t, current, tea.BlurMsg{})
		assertFraction(true)
	}
}

func TestContextFractionUsesCompactTokenCounts(t *testing.T) {
	for _, test := range []struct {
		usage DisplayContext
		want  string
	}{
		{DisplayContext{Tokens: 168000, Window: 1000000, Known: true}, "168K / 1M"},
		{DisplayContext{Tokens: 1500, Window: 500000, Known: true}, "1.5K / 500K"},
		{DisplayContext{Window: 500000, Known: true}, "0 / 500K"},
		{DisplayContext{Window: 500000}, "? / 500K"},
		{DisplayContext{Tokens: 1500, Known: true}, "1.5K / ?"},
		{DisplayContext{Tokens: 210000, Window: 200000, Known: true}, "210K / 200K"},
	} {
		current := newModel(nil, nil)
		current.contextUsage = test.usage
		if got := current.contextFraction(); got != test.want {
			t.Fatalf("fraction = %q, want %q", got, test.want)
		}
	}
}
