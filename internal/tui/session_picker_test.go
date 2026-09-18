package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type pickerBrowser struct{}

type waitingSessionBrowser struct{ started, cancelled chan struct{} }

func (b waitingSessionBrowser) SearchSessions(ctx context.Context, _ string) ([]interaction.SessionSummary, error) {
	close(b.started)
	<-ctx.Done()
	close(b.cancelled)
	return nil, ctx.Err()
}

func (b waitingSessionBrowser) PreviewSession(context.Context, string, string) (string, error) {
	panic("queued preview started after shutdown")
}

func TestSessionPickerQueryShutdown(t *testing.T) {
	t.Parallel()
	browser := waitingSessionBrowser{started: make(chan struct{}), cancelled: make(chan struct{})}
	search, preview, _, _, shutdown := sessionBrowserCommands(t.Context(), browser)
	command, cancel := search(1, "")
	defer cancel()
	queued, cancelQueued := preview(1, "one", "")
	defer cancelQueued()
	done := make(chan struct{})
	go func() { command(); close(done) }()
	select {
	case <-browser.started:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not start")
	}
	shutdown()
	select {
	case <-browser.cancelled:
	default:
		t.Fatal("shutdown returned before query cancellation")
	}
	<-done
	if result := queued().(sessionPreviewResult); !errors.Is(result.err, context.Canceled) {
		t.Fatal("late command was not cancelled")
	}
}

func TestSessionPickerCommandCannotBecomeSteering(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	m.closeSessionPicker()
	m.running = true
	m.input.SetValue("/history")
	m, command, _ := m.submitDelivery(deliverySteer)
	if command != nil || m.input.Value() != "/history" || !strings.Contains(m.inputNotice, "Stop") || len(m.pendingDeliveries) != 0 {
		t.Fatal("resume was accepted as steering")
	}
}

func (pickerBrowser) SearchSessions(ctx context.Context, _ string) ([]interaction.SessionSummary, error) {
	return []interaction.SessionSummary{{Key: "one", ID: "one", Title: "中文恢复 original", UpdatedAt: 100},
		{Key: "two", ID: "two", Title: "Second conversation", UpdatedAt: 200}}, ctx.Err()
}
func (pickerBrowser) PreviewSession(ctx context.Context, _, _ string) (string, error) {
	return "You\n中文问题\n\nAssistant\nA recovered answer", ctx.Err()
}

func pickerModel(t *testing.T, width, height int) model {
	t.Helper()
	m := newModel(make(chan runRequest), make(chan struct{}), SlashCommand{Name: "history"})
	m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	var closeQueries func()
	m.searchSessions, m.previewSession, m.readSession, m.renameSession, closeQueries = sessionBrowserCommands(t.Context(), pickerBrowser{})
	t.Cleanup(closeQueries)
	m.input.SetValue("keep my draft")
	m, cmd, _ := m.openSessionPicker()
	next, preview := m.Update(cmd())
	m = next.(model)
	if preview != nil {
		m = updateModel(t, m, preview())
	}
	return m
}

func TestSessionPickerCancelPreservesDraftAndRejectsStaleResults(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	if !strings.Contains(ansi.Strip(m.View().Content), "中文恢复") {
		t.Fatal("list not visible")
	}
	oldGeneration := m.sessionQueryGeneration
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "nothing"})
	if len(m.sessionPicker.list.Items()) != 0 {
		t.Fatal("title filter did not apply immediately")
	}
	m = updateModel(t, m, sessionSearchResult{generation: oldGeneration, items: []interaction.SessionSummary{{Title: "STALE"}}})
	if len(m.sessionPicker.list.Items()) != 0 {
		t.Fatal("stale search overwrote current query")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.sessionPicker != nil || m.input.Value() != "keep my draft" {
		t.Fatal("cancel changed draft")
	}
	m = updateModel(t, m, sessionPreviewResult{generation: m.sessionPreviewGeneration - 1, text: "STALE"})
	if strings.Contains(m.View().Content, "STALE") {
		t.Fatal("closed picker received preview")
	}
}

func TestSessionPickerCanvasAndIME(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{24, 10}, {60, 24}, {100, 28}, {160, 40}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := pickerModel(t, size[0], size[1])
			for _, preview := range []bool{false, true} {
				m.sessionPicker.previewFocused = preview
				view := m.View()
				if lipgloss.Width(view.Content) != size[0] || lipgloss.Height(view.Content) != size[1] {
					t.Fatalf("canvas = %dx%d", lipgloss.Width(view.Content), lipgloss.Height(view.Content))
				}
				if !preview && (view.Cursor == nil || view.Cursor.X >= size[0] || view.Cursor.Y >= size[1]) {
					t.Fatal("lost search IME cursor")
				}
				if preview && view.Cursor != nil {
					t.Fatal("composer cursor leaked into preview")
				}
				assertCanvasBackground(t, view.Content, nil)
			}
		})
	}
}

func TestSessionPickerFailedRestoreKeepsConversation(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	m.entries = []transcriptEntry{{kind: entryUser, text: "original question"}}
	m.refreshViewport(true)
	next, _ := m.resumeSelectedSession()
	m = next.(model)
	if !m.running || !m.sessionPicker.restoring {
		t.Fatal("resume did not dispatch")
	}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true, err: errors.New("session disappeared")}}})
	if m.sessionPicker == nil || m.sessionPicker.restoring || m.running {
		t.Fatal("failed restore left UI busy")
	}
	if m.input.Value() != "keep my draft" || len(m.entries) != 1 || m.entries[0].text != "original question" {
		t.Fatal("failure changed original conversation")
	}
	if !strings.Contains(m.sessionPicker.notice, "disappeared") {
		t.Fatal("failure was hidden")
	}
}

func TestSessionPickerRestoreInstallsHistoryAndClearsTransientState(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	m.sessionPicker.restoring = true
	m.running = true
	m.entries = []transcriptEntry{{kind: entryUser, text: "old question"}}
	m.side.threads[1] = &sideThreadState{id: 1}
	view := &interaction.Transcript{SessionID: "two", ResetSideThreads: true, Entries: []interaction.TranscriptEntry{
		{Kind: interaction.TranscriptUser, ID: "user", Text: "restored question"},
		{Kind: interaction.TranscriptAssistant, ID: "assistant", Assistant: interaction.AssistantDisplay{Text: "restored answer", Concludes: true}},
	}}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true, output: "Cleanup warning: fixture", state: &RuntimeState{SessionID: "two", Transcript: view}}}})
	if m.sessionPicker != nil || m.running || m.input.Value() != "" || len(m.side.threads) != 0 {
		t.Fatal("transient state survived switch")
	}
	if len(m.entries) != 3 || m.entries[0].sourceID != "user" || m.sessionID != "two" || m.entries[2].text != "Cleanup warning: fixture" {
		t.Fatalf("wrong transcript: %#v", m.entries)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "restored answer") || !m.viewport.AtBottom() {
		t.Fatal("restored answer not visible at bottom")
	}
}

func TestSessionPickerCapturesInputAndMouse(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	l := m.sessionPickerLayout()
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 3, Y: l.y + 6, Button: tea.MouseLeft})
	if m.sessionPicker.list.Index() != 1 {
		t.Fatal("mouse did not select second item")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.sessionPicker.previewFocused {
		t.Fatal("tab did not enter preview")
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "do not insert"})
	if m.input.Value() != "keep my draft" || m.sessionPicker.input.Value() != "" {
		t.Fatal("preview paste escaped to composer")
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 55, Height: 18})
	if !strings.Contains(ansi.Strip(m.View().Content), "PREVIEW") {
		t.Fatal("narrow preview missing")
	}
}

func TestSessionPickerMovementKeepsPreviewAndCancelsOldWork(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	previous := m.sessionPicker.previewText
	next, old := m.handleSessionPicker(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if m.sessionPicker.list.Index() != 1 || m.sessionPicker.previewText != previous {
		t.Fatal("selection delayed or preview flashed")
	}
	next, latest := m.handleSessionPicker(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(model)
	result := old().(sessionPreviewResult)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatal("obsolete preview not cancelled")
	}
	m = updateModel(t, m, result)
	if m.sessionPicker.previewText != previous {
		t.Fatal("obsolete result replaced preview")
	}
	m = updateModel(t, m, latest())
	if m.sessionPicker.notice != "" {
		t.Fatal("loading notice remained")
	}
	// Pending search and preview must never gate Enter or Escape.
	m.sessionPicker.loading = true
	next, _ = m.resumeSelectedSession()
	if !next.(model).sessionPicker.restoring {
		t.Fatal("loading blocked restore")
	}
}

func TestSessionRelativeTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		age  time.Duration
		want string
	}{
		{"future", -time.Hour, "now"},
		{"seconds", 59 * time.Second, "now"},
		{"minute", time.Minute, "1min"},
		{"minutes", 59 * time.Minute, "59min"},
		{"hour", time.Hour, "1h"},
		{"day", 24 * time.Hour, "1d"},
		{"month", 30 * 24 * time.Hour, "1m"},
		{"year", 365 * 24 * time.Hour, "1y"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionRelativeTime(now.Add(-test.age).UnixMilli(), now); got != test.want {
				t.Fatalf("age = %q, want %q", got, test.want)
			}
		})
	}
}
