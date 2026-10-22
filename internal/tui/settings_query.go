package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type settingsReadResult struct {
	generation uint64
	snapshot   interaction.SettingsSnapshot
	err        error
}
type settingsSaveResult struct {
	generation uint64
	result     interaction.SettingsResult
	snapshot   interaction.SettingsSnapshot
	err        error
}

func settingsCommands(parent context.Context, reader interaction.SettingsReader, writer interaction.SettingsWriter) (
	func(uint64) (tea.Cmd, context.CancelFunc), func(uint64, interaction.SettingsRequest) tea.Cmd, func(),
) {
	ctx, cancel := context.WithCancel(parent)
	owner := &queryGate{}
	read := func(generation uint64) (tea.Cmd, context.CancelFunc) {
		readCtx, stop := context.WithCancel(ctx)
		return func() tea.Msg {
			if !owner.begin() {
				return settingsReadResult{generation: generation, err: context.Canceled}
			}
			defer owner.done()
			defer stop()
			snapshot, err := reader.ReadSettings(readCtx)
			return settingsReadResult{generation: generation, snapshot: snapshot, err: err}
		}, stop
	}
	save := func(generation uint64, request interaction.SettingsRequest) tea.Cmd {
		return func() tea.Msg {
			if !owner.begin() {
				return settingsSaveResult{generation: generation, err: context.Canceled}
			}
			defer owner.done()
			result, err := writer.ApplySettings(ctx, request)
			snapshot, _ := reader.ReadSettings(ctx)
			return settingsSaveResult{generation: generation, result: result, snapshot: snapshot, err: err}
		}
	}
	return read, save, func() { owner.close(cancel) }
}
