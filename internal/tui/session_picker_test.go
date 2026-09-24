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
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
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
				m.sessionPicker.previewVisible = preview
				if preview {
					m.sessionPicker.input.Blur()
				} else {
					m.sessionPicker.input.Focus()
				}
				m.resizeSessionPicker()
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
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
	if m.sessionPicker.previewFocused || m.sessionPicker.input.Focused() || m.View().Cursor != nil {
		t.Fatal("failed restore did not return focus to the list")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.sessionPicker.input.Focused() || m.sessionPicker.input.Value() != "" {
		t.Fatal("search shortcut failed after restoration error")
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
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 3, Y: l.y + 7, Button: tea.MouseLeft})
	if selectedSessionKey(m.sessionPicker) != "two" {
		t.Fatal("mouse did not select second item")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.sessionPicker.previewFocused {
		t.Fatal("right arrow did not enter preview")
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
	command := m.toggleSessionPreview()
	m = updateModel(t, m, command())
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	previous := m.sessionPicker.previewText
	next, old := m.handleSessionPicker(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if selectedSessionKey(m.sessionPicker) != "two" || m.sessionPicker.previewText != previous {
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

func TestSessionPickerPreviewIsOptIn(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 160, 40)
	if m.sessionPicker.previewText != "" || m.sessionPicker.previewVisible {
		t.Fatal("opening picker loaded a preview")
	}
	requested, cancelled := 0, 0
	m.previewSession = func(generation uint64, key, query string) (tea.Cmd, context.CancelFunc) {
		requested++
		return func() tea.Msg { return sessionPreviewResult{generation: generation, text: "Preview fixture"} },
			func() { cancelled++ }
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.PasteMsg{Content: "Second"})
	if requested != 0 || m.sessionPickerLayout().listWidth != m.sessionPickerLayout().inner {
		t.Fatal("hidden preview fetched data or consumed list space")
	}
	next, command := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(model)
	if requested != 1 || !m.sessionPicker.previewFocused || !m.sessionPickerLayout().wide {
		t.Fatal("right arrow did not open and request preview")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.sessionPicker.previewFocused || m.View().Cursor != nil {
		t.Fatal("list focus is missing or search retained the cursor")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if cancelled != 1 || m.sessionPicker.previewVisible || m.sessionPickerLayout().wide {
		t.Fatal("hiding preview did not cancel work and reclaim width")
	}
	m = updateModel(t, m, command())
	if strings.Contains(m.sessionPicker.previewText, "Preview fixture") {
		t.Fatal("late preview result was applied after hiding")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if requested != 1 {
		t.Fatal("hidden preview restarted on selection")
	}
}

func TestSessionPickerCloseButton(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{24, 10}, {60, 24}, {160, 40}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := pickerModel(t, size[0], size[1])
			mouse := sessionCloseMouse(t, m)
			if mouse.Y != m.sessionPickerLayout().y {
				t.Fatal("close button is not on the top border")
			}
			top, _, _ := strings.Cut(ansi.Strip(m.sessionPickerView()), "\n")
			if !strings.Contains(top, "HIST") || !strings.Contains(top, " [ ✘ ] ") ||
				ansi.StringWidth(top) != m.sessionPickerLayout().width {
				t.Fatalf("title or border width is wrong: %q", top)
			}
			idle := m.sessionPickerView()
			m = updateModel(t, m, tea.MouseMotionMsg(mouse))
			if m.sessionPickerView() == idle {
				t.Fatal("close button did not highlight on hover")
			}
			for _, press := range []bool{false, true} {
				if press {
					mouse.Button = tea.MouseLeft
					m = updateModel(t, m, tea.MouseClickMsg(mouse))
				}
				view := m.View().Content
				canvas := lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewLayer(view))
				for x := mouse.X - 2; x <= mouse.X+2; x++ {
					cell := canvas.CellAt(x, mouse.Y)
					assertColor(t, cell.Style.Fg, errorColor)
					assertColor(t, cell.Style.Bg, inkBlackColor)
				}
			}
			m = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
			if m.sessionPickerView() != idle {
				t.Fatal("close button retained hover after leaving")
			}
			mouse.Button = tea.MouseRight
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.sessionPicker == nil {
				t.Fatal("right click closed picker")
			}
			mouse.Button = tea.MouseLeft
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			if m.sessionPicker == nil {
				t.Fatal("release outside closed picker")
			}
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.sessionPicker == nil {
				t.Fatal("release without press closed picker")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.sessionPicker != nil || m.input.Value() != "keep my draft" {
				t.Fatal("close button failed in rename mode or lost draft")
			}
		})
	}
}

func TestSessionPickerCloseButtonCancelsRestore(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	m.sessionPicker.restoring = true
	cancelled := false
	m.cancelRun = func() { cancelled = true }
	mouse := sessionCloseMouse(t, m)
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	if !cancelled || m.sessionPicker == nil {
		t.Fatal("close during restore did not preserve restoration's cancellation lifecycle")
	}
}

func sessionCloseMouse(t *testing.T, m model) tea.Mouse {
	t.Helper()
	for y, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if x := strings.Index(row, "✘"); x >= 0 {
			return tea.Mouse{X: ansi.StringWidth(row[:x]), Y: y, Button: tea.MouseLeft}
		}
	}
	t.Fatal("close button not painted")
	return tea.Mouse{}
}

func TestSessionPickerClickIgnoresPagePadding(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 160, 40)
	items := make([]interaction.SessionSummary, 30)
	for i := range items {
		items[i] = interaction.SessionSummary{Key: fmt.Sprint(i), Title: fmt.Sprint(i)}
	}
	m.setSessionItems(items)
	l := m.sessionPickerLayout()
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 3, Y: l.y + 2 + l.bodyHeight, Button: tea.MouseLeft})
	if selectedSessionKey(m.sessionPicker) != "0" {
		t.Fatal("click on bottom padding selected an invisible item on the next page")
	}
}

func TestSessionPickerArrowFocusAndLayeredEscape(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		for _, closeFromList := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/list=%v", width, closeFromList), func(t *testing.T) {
				m := pickerModel(t, width, 28)
				m.sessionPicker.input.SetValue("query")
				m.sessionPicker.list.Select(2)
				for _, key := range []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift}, {Code: tea.KeyF3}} {
					m = updateModel(t, m, key)
				}
				if m.sessionPicker.previewVisible {
					t.Fatal("removed shortcuts opened preview")
				}
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
				generation := m.sessionPreviewGeneration
				for _, key := range []rune{tea.KeyRight, tea.KeyLeft, tea.KeyLeft, tea.KeyRight} {
					m = updateModel(t, m, tea.KeyPressMsg{Code: key})
					preview := key == tea.KeyRight
					if !m.sessionPicker.previewVisible || m.sessionPicker.previewFocused != preview {
						t.Fatal("arrow did not focus the corresponding pane")
					}
					if m.View().Cursor != nil || m.sessionPreviewGeneration != generation {
						t.Fatal("pane focus exposed the search cursor or reloaded preview")
					}
				}
				if closeFromList {
					m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
				}
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
				if m.sessionPicker == nil || m.sessionPicker.previewVisible || m.sessionPicker.previewFocused {
					t.Fatal("first Escape did not return to the full-width list")
				}
				if m.sessionPicker.input.Value() != "query" || selectedSessionKey(m.sessionPicker) != "two" {
					t.Fatal("preview navigation lost search or selection")
				}
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
				if m.sessionPicker != nil || m.input.Value() != "keep my draft" {
					t.Fatal("second Escape did not close picker and preserve draft")
				}
			})
		}
	}
}

func TestSessionPickerSlashFocusesSearch(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/preview=%v", width, preview), func(t *testing.T) {
				m := pickerModel(t, width, 28)
				if m.sessionPicker.input.Focused() || m.View().Cursor != nil {
					t.Fatal("picker should open with list focus")
				}
				if !strings.Contains(ansi.Strip(m.sessionPickerView()), "/ to Filter") ||
					!strings.Contains(ansi.Strip(m.sessionPickerView()), "/ search") {
					t.Fatal("search placeholder or shortcut hint missing")
				}
				if preview {
					m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
				}
				slash := tea.KeyPressMsg{Code: '/', Text: "/"}
				m = updateModel(t, m, slash)
				if !m.sessionPicker.input.Focused() || m.sessionPicker.previewFocused || m.View().Cursor == nil {
					t.Fatal("slash did not focus search from the active pane")
				}
				if m.sessionPicker.input.Value() != "" || m.sessionPicker.previewVisible != preview {
					t.Fatal("search shortcut inserted a slash or changed preview visibility")
				}
				m = updateModel(t, m, tea.PasteMsg{Content: "Second"})
				if selectedSessionKey(m.sessionPicker) != "two" {
					t.Fatal("focused search did not filter sessions")
				}
				m = updateModel(t, m, slash)
				if m.sessionPicker.input.Value() != "Second/" {
					t.Fatal("slash inside search was consumed as a shortcut")
				}
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				if m.sessionPicker.input.Focused() || m.View().Cursor != nil {
					t.Fatal("list navigation retained search focus")
				}
				m = updateModel(t, m, slash)
				if !m.sessionPicker.input.Focused() || m.sessionPicker.input.Value() != "Second/" {
					t.Fatal("refocusing search lost the existing query")
				}
			})
		}
	}
}

func TestSessionPickerSelectionAndPreviewShowFocus(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := pickerModel(t, width, 28)
			m.sessionPicker.previewText = "Last activity · 2026-09-18 12:34\n\nYou\nPreview content"
			m.resizeSessionPicker()
			for _, step := range []struct {
				key           rune
				list, preview bool
			}{
				{0, true, false},
				{tea.KeyRight, false, true},
				{tea.KeyLeft, true, false},
				{tea.KeyUp, true, false},
				{tea.KeyRight, false, true},
				{'/', false, false},
				{tea.KeyDown, true, false},
			} {
				if step.key != 0 {
					key := tea.KeyPressMsg{Code: step.key}
					if step.key == '/' {
						key.Text = "/"
					}
					m = updateModel(t, m, key)
				}
				// Keep the same activity payload while testing focus on both row types.
				m.sessionPicker.previewText = "Last activity · 2026-09-18 12:34\n\nYou\nPreview content"
				m.resizeSessionPicker()
				l := m.sessionPickerLayout()
				view := m.sessionPickerView()
				if lipgloss.Width(view) != l.width || lipgloss.Height(view) != l.height {
					t.Fatal("content overflowed the floating window")
				}
				plain := ansi.Strip(view)
				if strings.Count(plain, "╭") != 1 || strings.Count(plain, "╰") != 1 {
					t.Fatal("inner pane borders are still visible")
				}
				canvas := lipgloss.NewCanvas(l.width, l.height).Compose(lipgloss.NewLayer(view))
				if l.wide || !step.preview {
					want := primaryTextColor
					_, isGroup := m.sessionPicker.list.SelectedItem().(sessionGroupItem)
					if isGroup {
						want = informationColor
					}
					if step.list {
						want = secondaryColor
					}
					y := 3 + 2*(m.sessionPicker.list.Index()%m.sessionPicker.list.Paginator.PerPage)
					if isGroup {
						y++
						if strings.Contains(ansi.Strip(strings.Split(view, "\n")[y]), "›") {
							t.Fatal("group heading has a session selection marker")
						}
					}
					assertColor(t, canvas.CellAt(5, y).Style.Fg, want)
				}
				if l.wide || step.preview {
					x := 2
					if l.wide {
						x += l.listWidth + 3
						if canvas.CellAt(2+l.listWidth+1, 3).Content != "│" {
							t.Fatal("pane separator missing")
						}
					}
					want := primaryTextColor
					if step.preview {
						want = secondaryColor
					}
					assertColor(t, canvas.CellAt(x, 3).Style.Fg, want)
				}
			}
		})
	}
}

func TestSessionPickerDividerAndPaneClicks(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 160, 40)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	l := m.sessionPickerLayout()
	for x := l.listWidth; x < l.listWidth+3; x++ {
		m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 2 + x, Y: l.y + 5, Button: tea.MouseLeft})
		if selectedSessionKey(m.sessionPicker) != "one" || !m.sessionPicker.previewFocused {
			t.Fatal("divider click selected an item or changed focus")
		}
	}
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 5, Y: l.y + 5, Button: tea.MouseLeft})
	if m.sessionPicker.previewFocused || selectedSessionKey(m.sessionPicker) != "one" {
		t.Fatal("list click did not focus the painted item")
	}
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + l.listWidth + 5, Y: l.y + 3, Button: tea.MouseLeft})
	if !m.sessionPicker.previewFocused {
		t.Fatal("activity header click did not focus preview")
	}
}

func TestSessionPickerActivityHeaderStaysFixed(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		m := pickerModel(t, width, 24)
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		m = updateModel(t, m, sessionPreviewResult{generation: m.sessionPreviewGeneration,
			text: "Last activity · 2026-09-18 12:34\n\n" + strings.Repeat("Conversation line\n\n", 40) + "End of preview"})
		before := m.sessionPicker.preview.View()
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
		l := m.sessionPickerLayout()
		x := l.x + 2
		if l.wide {
			x += l.listWidth + 3
		}
		m = updateModel(t, m, tea.MouseWheelMsg{X: x, Y: l.y + 5, Button: tea.MouseWheelDown})
		for range 10 {
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		header, _, _ := strings.Cut(ansi.Strip(m.sessionPickerPreviewView(m.sessionPickerLayout())), "\n")
		if !strings.HasPrefix(header, "Last activity · 2026-09-18 12:34") || m.sessionPicker.preview.View() == before ||
			strings.Contains(m.sessionPicker.preview.View(), "Last activity") || !strings.Contains(ansi.Strip(m.sessionPicker.preview.View()), "End of preview") {
			t.Fatalf("activity header scrolled or preview body failed to scroll independently: width=%d header=%q body=%q", width, header, ansi.Strip(m.sessionPicker.preview.View()))
		}
	}
}

func TestSessionPickerSearchCursorUsesDisplayWidth(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"filter", "ab反对cd", "fdfdfsafsa反对舒服的爽肤水 反对反对的方法发"} {
		m := pickerModel(t, 120, 30)
		p := m.sessionPicker
		p.previewFocused = false
		p.input.Focus()
		p.input.SetValue(query)
		p.input.CursorEnd()
		m.resizeSessionPicker()
		_, cursor := m.overlaySessionPicker("")
		if cursor == nil {
			t.Fatalf("query %q: no cursor", query)
		}
		l := m.sessionPickerLayout()
		wantX := l.x + 2 + lipgloss.Width(p.input.Prompt) + lipgloss.Width(query)
		if cursor.X != wantX || cursor.Y != l.y+2 {
			t.Fatalf("query %q: cursor = (%d, %d), want (%d, %d)", query, cursor.X, cursor.Y, wantX, l.y+2)
		}
	}
}
