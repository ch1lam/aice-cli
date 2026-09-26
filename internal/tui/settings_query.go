package tui

import (
	"context"
	"sync"

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

type settingsQueryOwner struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func (o *settingsQueryOwner) begin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	o.wg.Add(1)
	return true
}

func settingsCommands(parent context.Context, reader interaction.SettingsReader, writer interaction.SettingsWriter) (
	func(uint64) (tea.Cmd, context.CancelFunc), func(uint64, interaction.SettingsRequest) tea.Cmd, func(),
) {
	ctx, cancel := context.WithCancel(parent)
	owner := &settingsQueryOwner{}
	read := func(generation uint64) (tea.Cmd, context.CancelFunc) {
		readCtx, stop := context.WithCancel(ctx)
		return func() tea.Msg {
			if !owner.begin() {
				return settingsReadResult{generation: generation, err: context.Canceled}
			}
			defer owner.wg.Done()
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
			defer owner.wg.Done()
			result, err := writer.ApplySettings(ctx, request)
			snapshot, _ := reader.ReadSettings(ctx)
			return settingsSaveResult{generation: generation, result: result, snapshot: snapshot, err: err}
		}
	}
	close := func() { owner.mu.Lock(); owner.closed = true; owner.mu.Unlock(); cancel(); owner.wg.Wait() }
	return read, save, close
}
