package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
)

func TestFormatRunDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{name: "negative", duration: -time.Second, expected: "0ms"},
		{name: "milliseconds", duration: 999 * time.Millisecond, expected: "999ms"},
		{name: "seconds", duration: time.Second, expected: "1s"},
		{
			name:     "seconds truncate subsecond",
			duration: 59*time.Second + 999*time.Millisecond,
			expected: "59s",
		},
		{name: "minutes and seconds", duration: time.Minute, expected: "1min 0s"},
		{
			name: "minutes and seconds truncate subsecond",
			duration: 59*time.Minute +
				42*time.Second +
				999*time.Millisecond,
			expected: "59min 42s",
		},
		{name: "hours and minutes", duration: time.Hour, expected: "1h 0min"},
		{
			name: "hours and minutes omit seconds",
			duration: 25*time.Hour +
				17*time.Minute +
				42*time.Second,
			expected: "25h 17min",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRunDuration(tt.duration); got != tt.expected {
				t.Errorf(
					"formatRunDuration(%s) = %q, want %q",
					tt.duration,
					got,
					tt.expected,
				)
			}
		})
	}
}

func TestModelRunDurationUpdatesAndStops(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().Add(-1500 * time.Millisecond)
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.activeProcessID = 1
	current.processGroups = []processGroup{{
		id:        1,
		startedAt: startedAt,
	}}

	if !current.updateActiveProcessDuration(startedAt.Add(1500 * time.Millisecond)) {
		t.Fatal("duration update did not change the displayed unit")
	}
	header := ansi.Strip(current.assistantHeaderView(1))
	if !strings.Contains(header, "✦  1s") {
		t.Fatalf("assistant header = %q, want stopped duration", header)
	}

	current.finishRun(nil)
	stoppedAt := current.processGroups[0].elapsed
	if current.updateActiveProcessDuration(time.Now().Add(time.Hour)) {
		t.Fatal("finished process duration continued updating")
	}
	if got := current.processGroups[0].elapsed; got != stoppedAt {
		t.Fatalf("finished duration = %s, want %s", got, stoppedAt)
	}
}

func TestModelSpinnerTickRefreshesRunDuration(t *testing.T) {
	t.Parallel()

	startedAt := time.Now()
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.width = 80
	current.height = 24
	current.running = true
	current.activeProcessID = 1
	current.assistantEntry = 0
	current.processGroups = []processGroup{{
		id:        1,
		startedAt: startedAt,
	}}
	current.entries = []transcriptEntry{{
		kind:      entryAssistant,
		text:      "partial response",
		processID: 1,
	}}
	current.resizeLayout()
	current.refreshViewport(true)

	updatedModel, _ := current.Update(spinner.TickMsg{
		Time: startedAt.Add(time.Second),
	})
	updated, ok := updatedModel.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updatedModel)
	}
	transcript := ansi.Strip(updated.viewport.GetContent())
	if !strings.Contains(transcript, "✧  1s") {
		t.Fatalf("updated transcript = %q, want refreshed duration", transcript)
	}
}
