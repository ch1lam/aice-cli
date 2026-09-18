package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionReadingInspectionLatestDirectoryAndReturn(t *testing.T) {
	m := pickerModel(t, 100, 28)
	m.entries = []transcriptEntry{{kind: entryUser, text: "Live question"}}
	m.refreshViewport(true)
	active := &interaction.Transcript{SessionID: "other", Entries: []interaction.TranscriptEntry{
		{ID: "u1", Kind: interaction.TranscriptUser, Text: "First question"},
		{ID: "a1", Kind: interaction.TranscriptAssistant, Assistant: interaction.AssistantDisplay{Text: "First answer", Concludes: true}},
		{ID: "u2", Kind: interaction.TranscriptUser, Text: "Latest question"},
		{ID: "a2", Kind: interaction.TranscriptAssistant, Assistant: interaction.AssistantDisplay{Text: "Latest answer", Concludes: true}},
	}}
	other := &interaction.Transcript{SessionID: "other", Entries: []interaction.TranscriptEntry{
		{ID: "abandoned", Kind: interaction.TranscriptUser, Text: "Abandoned match"},
	}}
	m = updateModel(t, m, sessionReadingResult{generation: m.sessionPreviewGeneration,
		view: &interaction.SessionReading{Transcript: other, Active: active, FocusID: "abandoned", OtherBranch: true}})
	if m.reading == nil || !strings.Contains(ansi.Strip(m.View().Content), "other branch") {
		t.Fatal("reader branch label missing")
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "never send"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "never send"})
	if m.input.Value() != "" || m.running {
		t.Fatal("reader accepted model input")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.reading.otherBranch || !strings.Contains(ansi.Strip(m.View().Content), "Latest answer") {
		t.Fatal("End did not return to latest active branch")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: 't', Text: "t"})
	if !m.reading.directory || !strings.Contains(m.viewport.GetContent(), "First question") {
		t.Fatal("questions directory missing")
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	if !strings.Contains(m.viewport.GetContent(), "First question") || m.View().Cursor != nil {
		t.Fatal("resize lost the directory or exposed a composer cursor")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.reading.directory || !strings.Contains(ansi.Strip(m.viewport.View()), "First question") {
		t.Fatal("directory did not jump to question")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.reading != nil || m.sessionPicker == nil || m.input.Value() != "keep my draft" || m.entries[0].text != "Live question" {
		t.Fatal("reader return changed live session")
	}
}

func TestSessionReadingLocatesKeywordInsideMessage(t *testing.T) {
	m := pickerModel(t, 100, 28)
	m.sessionPicker.input.SetValue("needle-at-end")
	view := &interaction.Transcript{SessionID: "match", Entries: []interaction.TranscriptEntry{
		{ID: "answer", Kind: interaction.TranscriptAssistant, Assistant: interaction.AssistantDisplay{
			Text: strings.Repeat("Earlier paragraph.\n\n", 80) + "needle-at-end", Concludes: true}},
	}}
	m = updateModel(t, m, sessionReadingResult{generation: m.sessionPreviewGeneration,
		view: &interaction.SessionReading{Transcript: view, Active: view, FocusID: "answer"}})
	if !strings.Contains(ansi.Strip(m.viewport.View()), "needle-at-end") {
		t.Fatal("reader stopped at message beginning instead of match")
	}
}

func TestCurrentTurnDirectoryPreservesDraft(t *testing.T) {
	m := pickerModel(t, 100, 28)
	m.closeSessionPicker()
	m.entries = []transcriptEntry{{kind: entryUser, text: "Question one"}, {kind: entryUser, text: "Question two"}}
	m.refreshViewport(true)
	m = m.openCurrentReading()
	if m.reading == nil || !m.reading.directory {
		t.Fatal("current question directory missing")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Value() != "keep my draft" || m.reading != nil {
		t.Fatal("draft changed")
	}
}
