package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const (
	permissionModeDefault = "default"
	permissionModeAuto    = "auto"
)

type permissionModeResult struct {
	mode string
	err  error
}

func permissionModeCommand(ctx context.Context, switcher interaction.PermissionModeSwitcher) func(string) tea.Cmd {
	return func(mode string) tea.Cmd {
		return func() tea.Msg {
			got, err := switcher.SetPermissionMode(ctx, mode)
			return permissionModeResult{mode: got, err: err}
		}
	}
}

// applyPermissionMode records the effective mode after a switch. The mode is
// state shown beside the model name, so only a refused switch says anything.
func (m model) applyPermissionMode(result permissionModeResult) model {
	if result.mode != "" {
		m.permissionMode = result.mode
	}
	if result.err != nil {
		m.inputNotice = "Permission mode unchanged: " + result.err.Error()
		m.resizeLayout()
	}
	m.refreshViewport(false)
	return m
}
