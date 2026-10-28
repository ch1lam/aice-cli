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
	if status := ansi.Strip(m.modelStatus(80)); !strings.Contains(status, "manual · deepseek-v4.1-flash") {
		t.Fatalf("manual status = %q", status)
	}
	if footer := ansi.Strip(m.statusLine(100)); !strings.Contains(footer, "Shift+Tab auto mode") {
		t.Fatalf("default footer = %q", footer)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(model)
	if cmd == nil || len(requested) != 1 || requested[0] != permissionModeAuto {
		t.Fatalf("Shift+Tab requested %v", requested)
	}
	m = updateModel(t, m, cmd())
	if m.permissionMode != permissionModeAuto || !strings.Contains(ansi.Strip(m.modelStatus(80)), "auto · deepseek-v4.1-flash") {
		t.Fatalf("mode %q status %q", m.permissionMode, ansi.Strip(m.modelStatus(80)))
	}
	if len(m.entries) != 0 {
		t.Fatalf("switch wrote transcript entries: %+v", m.entries)
	}
	if footer := ansi.Strip(m.statusLine(100)); !strings.Contains(footer, "Shift+Tab manual mode") {
		t.Fatalf("auto footer = %q", footer)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updateModel(t, updated.(model), cmd())
	if requested[1] != permissionModeDefault || m.permissionMode != permissionModeDefault || !strings.Contains(ansi.Strip(m.modelStatus(80)), "manual · ") {
		t.Fatalf("second toggle: %v mode %q", requested, m.permissionMode)
	}

	// A refused switch keeps the effective mode and says why beside the composer.
	m = updateModel(t, m, permissionModeResult{mode: permissionModeDefault, err: errors.New("--yolo cannot be combined with permission mode auto")})
	if m.permissionMode != permissionModeDefault || !strings.Contains(m.inputNotice, "unchanged: --yolo") || len(m.entries) != 0 {
		t.Fatalf("refused switch: %q %q %+v", m.permissionMode, m.inputNotice, m.entries)
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

func TestToolRowsShowAutoReview(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest, 1), make(chan struct{}))
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	base := transcriptEntry{kind: entryTool, toolName: "bash", toolDetail: "make build", toolDone: true, toolOutput: interaction.ToolOutputDisplay{Text: "ok", Available: true}}

	allowed := base
	allowed.toolReview = &interaction.ReviewDisplay{Reason: "user asked\x1b[31m to build"}
	if header := ansi.Strip(m.toolHeaderView(allowed)); !strings.HasSuffix(header, "auto") {
		t.Fatalf("allowed header = %q", header)
	}
	if body := ansi.Strip(m.toolBodyView(allowed)); !strings.Contains(body, "auto mode allowed: user asked") || strings.Contains(body, "\x1b[31m") {
		t.Fatalf("allowed body = %q", body)
	}

	blocked := base
	blocked.toolError = true
	blocked.toolReview = &interaction.ReviewDisplay{Blocked: true, Reason: "not requested"}
	if header := ansi.Strip(m.toolHeaderView(blocked)); !strings.HasSuffix(header, "auto · blocked") {
		t.Fatalf("blocked header = %q", header)
	}
	if body := ansi.Strip(m.toolBodyView(blocked)); strings.Contains(body, "auto mode allowed") {
		t.Fatalf("blocked body = %q", body)
	}
	if header := ansi.Strip(m.toolHeaderView(base)); strings.Contains(header, "auto") {
		t.Fatalf("unreviewed header = %q", header)
	}
}
