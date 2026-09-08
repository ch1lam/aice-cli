package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type fileCompletionState struct {
	ref        interaction.FileReference
	items      []interaction.FileCompletion
	selection  int
	generation uint64
	dismissed  bool
	pending    bool
	cancel     context.CancelFunc
}

type fileCompletionResult struct {
	generation uint64
	items      []interaction.FileCompletion
	err        error
}

func fileCompletionCommand(ctx context.Context, completer interaction.FileCompleter) func(uint64, string) (tea.Cmd, context.CancelFunc) {
	return func(generation uint64, query string) (tea.Cmd, context.CancelFunc) {
		searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		return func() tea.Msg {
			defer cancel()
			timer := time.NewTimer(120 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-searchCtx.Done():
				return fileCompletionResult{generation: generation, err: searchCtx.Err()}
			case <-timer.C:
			}
			items, err := completer.CompleteFiles(searchCtx, query)
			return fileCompletionResult{generation: generation, items: items, err: err}
		}, cancel
	}
}

func (m model) fileReferenceAtCursor() (interaction.FileReference, bool) {
	if m.completeFiles == nil || m.side.isVisible || m.secretInput != nil || m.commandMenu != nil || !m.composerInputEnabled() {
		return interaction.FileReference{}, false
	}
	rows := m.composerRows()
	row := m.input.Line()
	if row >= len(rows) {
		return interaction.FileReference{}, false
	}
	cursor := m.input.Column()
	for i := 0; i < row; i++ {
		cursor += len([]rune(rows[i])) + 1
	}
	runes := []rune(m.input.Value())
	cursor = min(cursor, len(runes))
	refs := interaction.ScanFileReferences(string(runes[:cursor]))
	if len(refs) == 0 {
		return interaction.FileReference{}, false
	}
	ref := refs[len(refs)-1]
	if ref.End != cursor {
		return interaction.FileReference{}, false
	}
	// A closed quoted token is ready to send; do not reopen its suggestion menu.
	if ref.Complete && ref.End > ref.Start+1 && (runes[ref.Start+1] == '"' || runes[ref.Start+1] == '\'') {
		return interaction.FileReference{}, false
	}
	return ref, true
}

func (m *model) requestFileCompletion() tea.Cmd {
	ref, ok := m.fileReferenceAtCursor()
	if ok && ref == m.fileCompletion.ref {
		return nil
	}
	if m.fileCompletion.cancel != nil {
		m.fileCompletion.cancel()
	}
	generation := m.fileCompletion.generation + 1
	previous := m.fileCompletion
	m.fileCompletion = fileCompletionState{generation: generation}
	if !ok {
		return nil
	}
	m.fileCompletion.ref = ref
	// Keep the menu mounted while the same token is being edited. Removing it
	// during every debounce interval resizes and repaints the transcript twice.
	if ref.Start == previous.ref.Start && !previous.dismissed {
		m.fileCompletion.items = previous.items
		m.fileCompletion.selection = previous.selection
	}
	m.fileCompletion.pending = true
	command, cancel := m.completeFiles(generation, ref.Path)
	m.fileCompletion.cancel = cancel
	return command
}

func (m model) fileCompletionVisible() bool {
	ref, ok := m.fileReferenceAtCursor()
	return ok && ref == m.fileCompletion.ref && !m.fileCompletion.dismissed && len(m.fileCompletion.items) > 0
}

func (m model) fileCompletionView(width int) string {
	if !m.fileCompletionVisible() {
		return ""
	}
	rows := make([]slashMenuRow, len(m.fileCompletion.items))
	for i, item := range m.fileCompletion.items {
		rows[i] = slashMenuRow{label: sanitizeToolDetail(item.Path, false)}
	}
	return renderSlashMenuRows(width, "FILES", "↑/↓ select · tab attach · esc close", rows, m.fileCompletion.selection)
}

func (m model) handleFileCompletionKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.fileCompletionVisible() {
		return m, nil, false
	}
	switch message.Code {
	case tea.KeyUp:
		m.fileCompletion.selection = (m.fileCompletion.selection + len(m.fileCompletion.items) - 1) % len(m.fileCompletion.items)
	case tea.KeyDown:
		m.fileCompletion.selection = (m.fileCompletion.selection + 1) % len(m.fileCompletion.items)
	case tea.KeyEscape:
		m.fileCompletion.dismissed = true
	case tea.KeyTab:
		if m.fileCompletion.pending {
			return m, nil, true
		}
		item := m.fileCompletion.items[m.fileCompletion.selection]
		ref := m.fileCompletion.ref
		for _, full := range interaction.ScanFileReferences(m.input.Value()) {
			if full.Start == ref.Start {
				ref.End = full.End
				break
			}
		}
		runes := []rune(m.input.Value())
		replacement := interaction.QuoteFileReference(item.Path)
		if item.Directory {
			replacement = strings.TrimSuffix(replacement, "\"")
		}
		tail := string(runes[ref.End:])
		m.input.SetValue(string(runes[:ref.Start]) + replacement + tail)
		for range []rune(tail) {
			m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		}
		m.fileCompletion.dismissed = true
		command := m.requestFileCompletion()
		m.resizeLayout()
		m.refreshViewport(false)
		return m, command, true
	default:
		return m, nil, false
	}
	m.resizeLayout()
	m.refreshViewport(false)
	return m, nil, true
}
