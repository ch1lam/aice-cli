package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type settingsStatusResult struct {
	generation, revision uint64
	field                interaction.SettingField
	err                  error
}

func settingsStatusCommands(parent context.Context, reader interaction.SettingsStatusReader) (func(uint64, uint64) (tea.Cmd, context.CancelFunc), func()) {
	ctx, cancel := context.WithCancel(parent)
	owner := &settingsQueryOwner{}
	read := func(generation, revision uint64) (tea.Cmd, context.CancelFunc) {
		readCtx, stop := context.WithCancel(ctx)
		return func() tea.Msg {
			defer stop()
			if !owner.begin() {
				return settingsStatusResult{generation: generation, revision: revision, err: context.Canceled}
			}
			defer owner.wg.Done()
			field, err := reader.ReadSettingsStatus(readCtx, revision)
			return settingsStatusResult{generation: generation, revision: revision, field: field, err: err}
		}, stop
	}
	return read, func() {
		owner.mu.Lock()
		owner.closed = true
		owner.mu.Unlock()
		cancel()
		owner.wg.Wait()
	}
}

func (m *model) refreshSettingsStatus() tea.Cmd {
	p := m.settings
	if p == nil || p.usage || m.readSettingsStatus == nil || p.snapshot.Categories == nil {
		return nil
	}
	if p.cancelStatus != nil {
		p.cancelStatus()
	}
	command, cancel := m.readSettingsStatus(p.generation, p.snapshot.Revision)
	p.cancelStatus = cancel
	return command
}

func (m model) applySettingsStatus(message settingsStatusResult) (tea.Model, tea.Cmd) {
	p := m.settings
	if p == nil || p.usage || p.generation != message.generation || p.snapshot.Revision != message.revision {
		return m, nil
	}
	p.cancelStatus = nil
	if errors.Is(message.err, context.Canceled) {
		return m, nil
	}
	field := message.field
	if message.err != nil {
		field.Value.Text = "Unknown"
		field.Description = "Status unavailable: " + message.err.Error() + ". Refresh to retry."
	}
	// Only replace the status row. Navigation, drafts, notices and the saved
	// revision belong to the foreground interaction and must survive this read.
	for i, previous := range p.snapshot.Fields {
		if previous.ID != field.ID || previous.Kind != interaction.SettingInfo {
			continue
		}
		selected := ""
		if fields := p.fields(); p.selection < len(fields) {
			selected = fields[p.selection].ID
		}
		p.snapshot.Fields = slices.Clone(p.snapshot.Fields)
		p.snapshot.Fields[i] = field
		if p.editing != nil && p.editing.ID == field.ID && p.editing.Kind == interaction.SettingInfo {
			p.editing = &field
		}
		fields := p.fields()
		p.selection = min(p.selection, max(0, len(fields)-1))
		for j, candidate := range fields {
			if candidate.ID == selected {
				p.selection = j
				break
			}
		}
		break
	}
	return m, nil
}
