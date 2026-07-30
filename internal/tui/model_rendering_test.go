package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestModelRendersOneAICEHeadingPerProcess(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.width = 80
	processID := current.beginProcess()
	current.entries = []transcriptEntry{
		{
			kind:      entryAssistant,
			thinking:  "FIRST_REASONING",
			text:      "FIRST_OUTPUT",
			complete:  true,
			processID: processID,
		},
		{
			kind:      entryTool,
			processID: processID,
			toolName:  "read",
			toolDone:  true,
		},
		{
			kind:       entryAssistant,
			thinking:   "FINAL_REASONING",
			text:       "FINAL_OUTPUT",
			complete:   true,
			processID:  processID,
			conclusion: true,
		},
	}

	transcript := ansi.Strip(current.transcriptView())
	if got := strings.Count(transcript, "✦ AICE"); got != 1 {
		t.Fatalf("AICE headings = %d, want 1:\n%s", got, transcript)
	}
	assertTranscriptGap(t, transcript, "FIRST_OUTPUT", "read", 2)
	assertTranscriptGap(t, transcript, "read", "FINAL_REASONING", 2)
	assertTranscriptGap(t, transcript, "FINAL_REASONING", "FINAL_OUTPUT", 2)
}

func TestModelOmitsEmptyAssistantPlaceholder(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.entries = []transcriptEntry{
		{kind: entryUser, text: "inspect"},
		{kind: entryAssistant, complete: true},
	}

	transcript := ansi.Strip(current.transcriptView())
	if strings.Contains(transcript, "Waiting for model output...") {
		t.Fatalf("empty assistant placeholder remains visible: %q", transcript)
	}
}
