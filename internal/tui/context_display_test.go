package tui

import (
	"strings"
	"testing"

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
		line := current.statusLine(width)
		if !strings.Contains(ansi.Strip(line), "25.00%") {
			t.Fatalf("width %d: %q", width, line)
		}
		if lipgloss.Width(line) > width || lipgloss.Height(line) != 1 {
			t.Fatalf("wrapped at %d: %q", width, line)
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
