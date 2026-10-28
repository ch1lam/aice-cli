package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestShiftTabTogglesPermissionMode(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest, 1), make(chan struct{}))
	m.currentModel = interaction.DisplayModel{ID: "deepseek-v4.1-flash"}
	m.permissionMode = permissionModeDefault
	var requested []string
	m.setPermissionMode = func(mode string) tea.Cmd {
		requested = append(requested, mode)
		return func() tea.Msg { return permissionModeResult{mode: mode} }
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(model)
	if cmd == nil || len(requested) != 1 || requested[0] != permissionModeAuto {
		t.Fatalf("Shift+Tab requested %v", requested)
	}
	m = updateModel(t, m, cmd())
	if m.permissionMode != permissionModeAuto || !strings.Contains(ansi.Strip(m.modelStatus(80)), "auto · deepseek-v4.1-flash") {
		t.Fatalf("mode %q status %q", m.permissionMode, ansi.Strip(m.modelStatus(80)))
	}
	if last := m.entries[len(m.entries)-1]; last.kind != entryNotice || !strings.Contains(last.text, "Permission mode: auto") {
		t.Fatalf("notice = %+v", last)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updateModel(t, updated.(model), cmd())
	if requested[1] != permissionModeDefault || m.permissionMode != permissionModeDefault || strings.Contains(ansi.Strip(m.modelStatus(80)), "auto") {
		t.Fatalf("second toggle: %v mode %q", requested, m.permissionMode)
	}

	// A refused switch keeps the effective mode and says why.
	m = updateModel(t, m, permissionModeResult{mode: permissionModeDefault, err: errors.New("--yolo cannot be combined with permission mode auto")})
	if m.permissionMode != permissionModeDefault || !strings.Contains(m.entries[len(m.entries)-1].text, "unchanged: --yolo") {
		t.Fatalf("refused switch: %q %+v", m.permissionMode, m.entries[len(m.entries)-1])
	}
}

func TestShiftTabUnavailableWithoutSwitcher(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest, 1), make(chan struct{}))
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}); cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(permissionModeResult); ok {
				t.Fatal("switched without a capability")
			}
		}
	}
}
