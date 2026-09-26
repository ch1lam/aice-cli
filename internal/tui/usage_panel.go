package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func usageCommands(parent context.Context, reader interaction.UsageReader) (func(uint64) (tea.Cmd, context.CancelFunc), func()) {
	ctx, cancel := context.WithCancel(parent)
	owner := &settingsQueryOwner{}
	read := func(generation uint64) (tea.Cmd, context.CancelFunc) {
		ctx, stop := context.WithCancel(ctx)
		return func() tea.Msg {
			if !owner.begin() {
				return settingsReadResult{generation: generation, err: context.Canceled}
			}
			defer owner.wg.Done()
			defer stop()
			snapshot, err := reader.ReadUsage(ctx)
			return settingsReadResult{generation: generation, snapshot: usageFields(snapshot), err: err}
		}, stop
	}
	return read, func() { owner.mu.Lock(); owner.closed = true; owner.mu.Unlock(); cancel(); owner.wg.Wait() }
}
func usageFields(u interaction.UsageSnapshot) interaction.SettingsSnapshot {
	s := interaction.SettingsSnapshot{Revision: u.Revision, Categories: []interaction.SettingCategory{{ID: "context", Label: "Context"}, {ID: "usage", Label: "Session usage"}, {ID: "session", Label: "Session info"}}}
	add := func(category, label, value string) {
		s.Fields = append(s.Fields, interaction.SettingField{ID: category + label, Category: category, Label: label, Kind: interaction.SettingInfo, Value: interaction.SettingValue{Text: value}, Description: value})
	}
	tokens := "Unknown"
	if u.Context.Known {
		tokens = fmt.Sprintf("%d tokens", u.Context.Tokens)
		if u.Context.Estimated {
			tokens += " (estimated)"
		}
	}
	add("context", "Occupancy", tokens)
	capacity := "Unknown"
	if u.Context.Window > 0 {
		capacity = fmt.Sprintf("%d tokens", u.Context.Window)
		if u.Context.Known {
			capacity += fmt.Sprintf(" · %.1f%%", 100*float64(u.Context.Tokens)/float64(u.Context.Window))
		}
	}
	add("context", "Capacity", capacity)
	add("context", "Window source", u.WindowSource)
	add("context", "Provider / model", u.Provider+" / "+u.Model)
	add("usage", "Input tokens", fmt.Sprint(u.Usage.InputTokens))
	add("usage", "Output tokens", fmt.Sprint(u.Usage.OutputTokens))
	add("usage", "Reasoning tokens (output subset)", fmt.Sprint(u.ReasoningTokens))
	add("usage", "Cache read / write", fmt.Sprintf("%d / %d", u.Usage.CacheReadTokens, u.Usage.CacheWriteTokens))
	cost := u.CostStatus
	if cost != "Unavailable" {
		cost = fmt.Sprintf("$%.6f · %s", u.Usage.TotalCost, cost)
	}
	add("usage", "Recorded cost", cost)
	add("usage", "Scope", u.Scope)
	if u.SessionID == "" {
		add("session", "Session", "Not started; no Session file has been created")
		add("usage", "Session", "Not started")
	} else {
		add("session", "Session ID", u.SessionID)
		add("session", "Path", u.Path)
		add("session", "Active branch leaf", u.LeafID)
		add("session", "Nodes / messages / compactions", fmt.Sprintf("%d / %d / %d", u.Nodes, u.Messages, u.Compactions))
		add("session", "Created", time.UnixMilli(u.CreatedAt).Format(time.RFC3339))
	}
	add("session", "Working directory", u.Directory)
	add("session", "Current model", u.Provider+" / "+u.Model)
	for _, category := range []string{"context", "usage", "session"} {
		add(category, "Read at", u.ReadAt.Format(time.RFC3339))
	}
	return s
}
func (m model) openUsage(tab int) (model, tea.Cmd, bool) {
	if m.readUsage == nil {
		return m, nil, true
	}
	next, command, _ := m.openSettingsPanel(true)
	next.settings.tab = tab
	return next, command, true
}
func (m model) isInfoCommandInput() bool {
	request, ok := parseSlashCommand(m.input.Value())
	if !ok || request.Arguments != "" {
		return false
	}
	return (request.Name == "settings" && m.readSettings != nil) || ((request.Name == "usage" || request.Name == "context" || request.Name == "session") && m.readUsage != nil)
}
