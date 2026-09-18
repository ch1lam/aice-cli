package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type sessionPicker struct {
	input                       textinput.Model
	list                        list.Model
	preview                     viewport.Model
	all                         []interaction.SessionSummary
	previewText                 string
	previewFocused              bool
	loading, restoring          bool
	notice                      string
	cancelSearch, cancelPreview context.CancelFunc
}

type sessionSearchResult struct {
	next       tea.Cmd
	generation uint64
	query      string
	items      []interaction.SessionSummary
	err        error
}

type sessionPreviewResult struct {
	generation uint64
	text       string
	err        error
}

// Bubble Tea owns command execution; every operation inherits program
// cancellation and also has a per-query cancellation/deadline.
func sessionBrowserCommands(ctx context.Context, browser interaction.SessionBrowser) (
	func(uint64, string) (tea.Cmd, context.CancelFunc),
	func(uint64, string, string) (tea.Cmd, context.CancelFunc),
	func(uint64, string, string) (tea.Cmd, context.CancelFunc),
	func(),
) {
	ctx, cancelAll := context.WithCancel(ctx)
	var owner sessionQueryOwner
	search := func(generation uint64, query string) (tea.Cmd, context.CancelFunc) {
		searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		return sessionSearchCommand(searchCtx, cancel, &owner, browser, generation, query), cancel
	}
	preview := func(generation uint64, key, query string) (tea.Cmd, context.CancelFunc) {
		previewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		return func() tea.Msg {
			defer cancel()
			if !owner.begin() {
				return sessionPreviewResult{generation: generation, err: context.Canceled}
			}
			defer owner.wg.Done()
			timer := time.NewTimer(120 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-previewCtx.Done():
				return sessionPreviewResult{generation: generation, err: previewCtx.Err()}
			case <-timer.C:
			}
			text, err := browser.PreviewSession(previewCtx, key, query)
			return sessionPreviewResult{generation: generation, text: text, err: err}
		}, cancel
	}
	read := func(generation uint64, key, focus string) (tea.Cmd, context.CancelFunc) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		return func() tea.Msg {
			defer cancel()
			if !owner.begin() {
				return sessionReadingResult{generation: generation, err: context.Canceled}
			}
			defer owner.wg.Done()
			reader, ok := browser.(interaction.SessionReader)
			if !ok {
				return sessionReadingResult{generation: generation, err: fmt.Errorf("history reading unavailable")}
			}
			view, err := reader.ReadSession(readCtx, key, focus)
			return sessionReadingResult{generation: generation, view: view, err: err}
		}, cancel
	}
	return search, preview, read, func() {
		owner.mu.Lock()
		owner.closed = true
		owner.mu.Unlock()
		cancelAll()
		owner.wg.Wait()
	}
}

// Commands can be queued when Bubble Tea exits. The gate prevents work from
// starting after shutdown begins, while the owner waits for started queries.
type sessionQueryOwner struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func (o *sessionQueryOwner) begin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	o.wg.Add(1)
	return true
}

type sessionListItem struct {
	interaction.SessionSummary
	current bool
	group   string
}

func (i sessionListItem) FilterValue() string { return i.Title }

type sessionItemDelegate struct{}

func (sessionItemDelegate) Height() int                         { return 3 }
func (sessionItemDelegate) Spacing() int                        { return 1 }
func (sessionItemDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (sessionItemDelegate) Render(w io.Writer, model list.Model, index int, item list.Item) {
	i, ok := item.(sessionListItem)
	if !ok {
		return
	}
	prefix := "  "
	style := bodyStyle
	if index == model.Index() {
		prefix = "› "
		style = labelStyle
	}
	title := sanitizeToolDetail(i.Title, false)
	if i.current {
		title = "● " + title
	}
	detail := time.UnixMilli(i.UpdatedAt).Local().Format("Jan 02 15:04")
	if i.Problem != "" {
		detail = "Unavailable · " + sanitizeToolDetail(i.Problem, false)
	}
	if i.OtherBranch {
		detail = "Other branch · resume active · " + detail
	}
	if i.Snippet != "" {
		detail += " · " + sanitizeToolDetail(strings.Join(strings.Fields(i.Snippet), " "), false)
	}
	_, _ = fmt.Fprint(w, mutedStyle.Render(i.group), "\n", style.Render(ansi.Truncate(prefix+title, model.Width(), "…")), "\n",
		mutedStyle.Render(ansi.Truncate("  "+detail, model.Width(), "…")))
}

func (m model) openSessionPicker() (model, tea.Cmd, bool) {
	if m.searchSessions == nil {
		return m, nil, true
	}
	if m.running || m.side.anyRunning() {
		m.inputNotice = "Stop the current response and BTW responses before switching sessions"
		return m.settleCommand(false, nil)
	}
	p := &sessionPicker{loading: true}
	p.input = textinput.New()
	p.input.Prompt = "› "
	p.input.Placeholder = "Search titles and conversation text…"
	p.input.CharLimit = 256
	p.input.SetVirtualCursor(false)
	p.input.Focus()
	p.list = list.New(nil, sessionItemDelegate{}, 30, 12)
	p.list.SetFilteringEnabled(false)
	p.list.SetShowTitle(false)
	p.list.SetShowStatusBar(false)
	p.list.SetShowHelp(false)
	p.list.SetShowPagination(false)
	p.list.DisableQuitKeybindings()
	p.preview = viewport.New()
	p.preview.FillHeight = true
	m.sessionPicker = p
	m.input.Blur()
	m.selection.clear()
	m.workspacePress = nil
	m.contextPressed = false
	m.copyNotice = false
	m.resizeSessionPicker()
	return m, m.requestSessionSearch(), true
}

func (m *model) closeSessionPicker() tea.Cmd {
	p := m.sessionPicker
	if p == nil {
		return nil
	}
	if p.cancelSearch != nil {
		p.cancelSearch()
	}
	if p.cancelPreview != nil {
		p.cancelPreview()
	}
	m.sessionQueryGeneration++
	m.sessionPreviewGeneration++
	m.sessionPicker = nil
	return m.input.Focus()
}

func (m *model) requestSessionSearch() tea.Cmd {
	p := m.sessionPicker
	if p.cancelSearch != nil {
		p.cancelSearch()
	}
	m.sessionQueryGeneration++
	p.loading = true
	p.notice = ""
	command, cancel := m.searchSessions(m.sessionQueryGeneration, p.input.Value())
	p.cancelSearch = cancel
	return command
}

func (m *model) setSessionItems(items []interaction.SessionSummary) {
	p := m.sessionPicker
	selected := ""
	if old, ok := p.list.SelectedItem().(sessionListItem); ok {
		selected = old.Key
	}
	rows := make([]list.Item, 0, len(items))
	index := 0
	lastGroup := ""
	now := time.Now()
	for i, item := range items {
		group := sessionDateGroup(item.UpdatedAt, now)
		heading := ""
		if group != lastGroup {
			heading = group
			lastGroup = group
		}
		rows = append(rows, sessionListItem{SessionSummary: item, current: item.ID != "" && item.ID == m.sessionID, group: heading})
		if item.Key == selected {
			index = i
		}
	}
	p.list.SetItems(rows)
	p.list.Select(index)
}

func (m *model) requestSessionPreview() tea.Cmd {
	p := m.sessionPicker
	if p.cancelPreview != nil {
		p.cancelPreview()
	}
	m.sessionPreviewGeneration++
	p.preview.GotoTop()
	item, ok := p.list.SelectedItem().(sessionListItem)
	if !ok {
		p.previewText = "Select a session to preview its recent conversation."
		m.resizeSessionPicker()
		return nil
	}
	if item.Problem != "" {
		p.previewText = item.Problem
		m.resizeSessionPicker()
		return nil
	}
	// Keep the previous preview during rapid movement; identify it explicitly.
	p.notice = "Loading selected preview…"
	if p.previewText == "" {
		p.previewText = "Loading preview…"
	}
	m.resizeSessionPicker()
	command, cancel := m.previewSession(m.sessionPreviewGeneration, item.Key, p.input.Value())
	p.cancelPreview = cancel
	return command
}

func (m model) applySessionSearch(result sessionSearchResult) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	if p == nil || p.restoring || result.generation != m.sessionQueryGeneration {
		return m, nil
	}
	p.loading = result.next != nil
	if result.err != nil {
		p.notice = result.err.Error()
		return m, nil
	}
	if result.query == "" {
		p.all = result.items
	}
	selected := selectedSessionKey(p)
	m.setSessionItems(result.items)
	if selected != "" && selectedSessionKey(p) == selected {
		return m, result.next
	}
	return m, tea.Batch(result.next, m.requestSessionPreview())
}

func (m model) applySessionPreview(result sessionPreviewResult) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	if p == nil || p.restoring || result.generation != m.sessionPreviewGeneration {
		return m, nil
	}
	p.notice = ""
	p.previewText = result.text
	if result.err != nil {
		p.previewText = result.err.Error()
	}
	m.resizeSessionPicker()
	return m, nil
}

func (m model) handleSessionPicker(message tea.Msg) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	if p.restoring {
		if key, ok := message.(tea.KeyPressMsg); ok && (key.Code == tea.KeyEscape || key.String() == "ctrl+c") {
			if m.cancelRun != nil {
				m.cancelRun()
			} else {
				m.cancelRequested = true
			}
		}
		return m, nil
	}
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc", "ctrl+c":
			return m, m.closeSessionPicker()
		case "tab", "shift+tab":
			p.previewFocused = !p.previewFocused
			if p.previewFocused {
				p.input.Blur()
			} else {
				p.input.Focus()
			}
			m.resizeSessionPicker()
			return m, nil
		case "f4":
			return m, m.requestSessionReading()
		case "enter":
			return m.resumeSelectedSession()
		case "up", "ctrl+p", "down", "ctrl+n", "pgup", "pgdown":
			if p.previewFocused {
				switch key.String() {
				case "up", "ctrl+p":
					p.preview.ScrollUp(1)
				case "down", "ctrl+n":
					p.preview.ScrollDown(1)
				case "pgup":
					p.preview.PageUp()
				case "pgdown":
					p.preview.PageDown()
				}
				return m, nil
			}
			old := p.list.Index()
			if key.String() == "up" || key.String() == "ctrl+p" {
				p.list.CursorUp()
			} else if key.String() == "down" || key.String() == "ctrl+n" {
				p.list.CursorDown()
			} else {
				p.list, _ = p.list.Update(key)
			}
			if old != p.list.Index() {
				return m, m.requestSessionPreview()
			}
			return m, nil
		}
	}
	if mouse, ok := message.(tea.MouseWheelMsg); ok {
		if p.previewFocused {
			if mouse.Button == tea.MouseWheelUp {
				p.preview.ScrollUp(3)
			} else {
				p.preview.ScrollDown(3)
			}
		} else {
			if mouse.Button == tea.MouseWheelUp {
				p.list.CursorUp()
			} else {
				p.list.CursorDown()
			}
			return m, m.requestSessionPreview()
		}
		return m, nil
	}
	if mouse, ok := message.(tea.MouseClickMsg); ok {
		return m.clickSessionPicker(mouse)
	}
	if p.previewFocused {
		return m, nil
	}
	before := p.input.Value()
	var command tea.Cmd
	p.input, command = p.input.Update(message)
	if before != p.input.Value() {
		query := strings.ToLower(strings.TrimSpace(p.input.Value()))
		var matches []interaction.SessionSummary
		for _, item := range p.all {
			if strings.Contains(strings.ToLower(item.Title), query) || strings.Contains(strings.ToLower(item.Key), query) {
				matches = append(matches, item)
			}
		}
		m.setSessionItems(matches)
		return m, tea.Batch(command, m.requestSessionSearch(), m.requestSessionPreview())
	}
	return m, command
}

func (m model) resumeSelectedSession() (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	item, ok := p.list.SelectedItem().(sessionListItem)
	if !ok {
		return m, nil
	}
	if item.Problem != "" {
		p.notice = item.Problem
		return m, nil
	}
	if item.current {
		command := m.closeSessionPicker()
		m.viewport.GotoBottom()
		return m, command
	}
	if m.running || m.side.anyRunning() {
		p.notice = "Stop responses before switching sessions"
		return m, nil
	}
	p.restoring = true
	if p.cancelSearch != nil {
		p.cancelSearch()
	}
	if p.cancelPreview != nil {
		p.cancelPreview()
	}
	m.sessionQueryGeneration++
	m.sessionPreviewGeneration++
	p.loading = false
	p.notice = "Restoring session…"
	p.input.Blur()
	m.running = true
	m.acceptsDelivery = false
	m.status = "Restoring session…"
	return m, startSlashCommand(m.requests, m.controllerDone, SlashCommandRequest{Name: "history", Arguments: item.Key})
}
