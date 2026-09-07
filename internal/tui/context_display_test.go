package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestContextStatusRemainingPercentage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		usage DisplayContext
		want  string
	}{
		{"small window", DisplayContext{Tokens: 50000, Window: 200000, Known: true}, "ctx 75.0% left / 200k"},
		{"long window", DisplayContext{Tokens: 50000, Window: 1000000, Known: true}, "ctx 95.0% left / 1.0M"},
		{"estimated", DisplayContext{Tokens: 50000, Window: 200000, Known: true, Estimated: true}, "ctx ~75.0% left / 200k"},
		{"overflow", DisplayContext{Tokens: 210000, Window: 200000, Known: true}, "ctx 0.0% left / 200k"},
		{"empty", DisplayContext{Window: 200000, Known: true}, "ctx 100.0% left / 200k"},
		{"unknown model", DisplayContext{Known: true}, "ctx ?"},
		{"unknown usage", DisplayContext{Window: 200000}, "ctx ?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := newModel(nil, nil)
			current.contextUsage = test.usage
			if got := ansi.Strip(current.contextStatus(true)); got != test.want {
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
		if !strings.Contains(ansi.Strip(line), "ctx 75.0% left") {
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
	if !strings.Contains(current.contextStatus(true), "95.0%") {
		t.Fatal("model switch kept old percentage")
	}
}
