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

// applyPermissionMode shows the effective mode after a switch.
func (m model) applyPermissionMode(result permissionModeResult) model {
	if result.mode != "" {
		m.permissionMode = result.mode
	}
	text := "Permission mode: default. You confirm actions outside the workspace or with consequences."
	if m.permissionMode == permissionModeAuto {
		text = "Permission mode: auto. The action classifier decides eligible tool calls; you are asked only at its limits."
	}
	if result.err != nil {
		text = "Permission mode unchanged: " + result.err.Error()
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryNotice, text: text})
	m.refreshViewport(true)
	return m
}
