package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestInputActionsReserveDisabledShortcuts(t *testing.T) {
	t.Parallel()
	for _, shortcut := range []rune{'r', 't', 'g'} {
		t.Run(string(shortcut), func(t *testing.T) {
			current := newModel(nil, nil)
			current.running, current.acceptsDelivery = true, true
			current.input.SetValue("draft")
			message := tea.KeyPressMsg{Code: shortcut, Mod: tea.ModCtrl}
			match := current.matchInputAction(message)
			if !match.matched || match.enabled || match.blocked != inputActionUnavailable {
				t.Fatalf("running shortcut match = %#v", match)
			}
			updated := updateModel(t, current, message)
			if updated.input.Value() != "draft" || updated.sessionPicker != nil || updated.reading != nil {
				t.Fatal("disabled shortcut changed the draft or opened another domain")
			}
		})
	}
}

func TestInputActionsKeepPrintableHelpAndArrowsInEditor(t *testing.T) {
	t.Parallel()
	current := newModel(nil, nil)
	current.input.SetValue("first\nsecond")
	current.input.CursorEnd()
	for _, message := range []tea.KeyPressMsg{
		{Code: '?', Text: "?"}, {Code: tea.KeyUp}, {Code: tea.KeyDown},
	} {
		if match := current.matchInputAction(message); match.matched {
			t.Fatalf("draft editing key %q became action %#v", message.String(), match)
		}
	}
	updated := updateModel(t, current, tea.KeyPressMsg{Code: '?', Text: "?"})
	if updated.help.ShowAll || updated.input.Value() != "first\nsecond?" {
		t.Fatalf("question mark was not inserted once: %q", updated.input.Value())
	}
}

func TestInputActionHelpTracksClipboardAndDelivery(t *testing.T) {
	t.Parallel()
	current := newModel(nil, nil)
	current.running, current.acceptsDelivery = true, true
	current.help.ShowAll = true
	current.clipboard = func() tea.Msg { return nil }
	current.searchSessions = func(uint64, string) (tea.Cmd, context.CancelFunc) { return nil, nil }
	if text := ansi.Strip(current.footerView(160)); !strings.Contains(text, "steer") || !strings.Contains(text, "queue") {
		t.Fatalf("running agent help lost delivery actions: %q", text)
	}
	current.clipboardPending = true
	message := tea.KeyPressMsg{Code: tea.KeyEnter}
	match := current.matchInputAction(message)
	if !match.matched || match.enabled || match.blocked != inputActionClipboardPending {
		t.Fatalf("clipboard match = %#v", match)
	}
	if text := ansi.Strip(current.footerView(160)); strings.Contains(text, "steer") || strings.Contains(text, "queue") {
		t.Fatalf("clipboard help advertised blocked submission: %q", text)
	}
	updated := updateModel(t, current, message)
	if !strings.Contains(updated.inputNotice, "Reading clipboard") {
		t.Fatal("blocked submission lost the clipboard explanation")
	}
	current.clipboardPending = false
	current.deliveryPending = true
	cancelled := false
	current.cancelDelivery = func() { cancelled = true }
	updated = updateModel(t, current, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !cancelled || updated.clearQuitPending {
		t.Fatal("Ctrl+C failed to cancel delivery or armed quit")
	}
	if text := ansi.Strip(current.footerView(160)); !strings.Contains(text, "cancel delivery") || strings.Contains(text, "Enter steer") {
		t.Fatalf("delivery help = %q", text)
	}
}

func TestSideInputActionHelpFollowsThreadAvailability(t *testing.T) {
	t.Parallel()
	current := newModel(nil, nil)
	current.side.isVisible = true
	current.side.activeID = 1
	thread := &sideThreadState{id: 1, status: interaction.SideThreadReadOnly}
	current.side.threads = map[uint64]*sideThreadState{1: thread}
	current.help.ShowAll = true
	current.input.SetValue("preserved draft")
	for _, message := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: 'g', Mod: tea.ModCtrl}, {Code: 'r', Mod: tea.ModCtrl},
	} {
		match := current.matchInputAction(message)
		if !match.matched || match.enabled {
			t.Fatalf("read-only action %q = %#v", message.String(), match)
		}
		current = updateModel(t, current, message)
	}
	text := ansi.Strip(current.footerView(240))
	if strings.Contains(text, "Enter ask") || strings.Contains(text, "newline") || strings.Contains(text, "editor") ||
		!strings.Contains(text, "Esc close") || !strings.Contains(text, "Ctrl+d end thread") {
		t.Fatalf("read-only help = %q", text)
	}
	if current.input.Value() != "preserved draft" {
		t.Fatal("read-only reserved keys changed draft")
	}
	thread.isRunning = true
	if text := ansi.Strip(current.footerView(240)); !strings.Contains(text, "Esc cancel") {
		t.Fatalf("running side help = %q", text)
	}
}
