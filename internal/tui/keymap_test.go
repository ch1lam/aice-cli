package tui

import "testing"

func TestKeyMapForInputContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		running           bool
		acceptsDelivery   bool
		wantSendEnabled   bool
		wantQueueEnabled  bool
		wantSendHelp      string
		wantQuitEnabled   bool
		wantInterruptHelp string
	}{
		{
			name:              "idle",
			wantSendEnabled:   true,
			wantSendHelp:      "send",
			wantQuitEnabled:   true,
			wantInterruptHelp: "cancel",
		},
		{
			name:              "running",
			running:           true,
			wantSendEnabled:   false,
			wantSendHelp:      "send",
			wantQuitEnabled:   false,
			wantInterruptHelp: "cancel",
		},
		{
			name:              "running agent",
			running:           true,
			acceptsDelivery:   true,
			wantSendEnabled:   true,
			wantQueueEnabled:  true,
			wantSendHelp:      "steer",
			wantQuitEnabled:   false,
			wantInterruptHelp: "cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newModel(nil, nil)
			current.running, current.acceptsDelivery = tt.running, tt.acceptsDelivery
			keys := current.inputActionKeys()
			if keys.send.Enabled() != tt.wantSendEnabled {
				t.Errorf("send enabled = %v, want %v", keys.send.Enabled(), tt.wantSendEnabled)
			}
			if keys.quit.Enabled() != tt.wantQuitEnabled {
				t.Errorf("quit enabled = %v, want %v", keys.quit.Enabled(), tt.wantQuitEnabled)
			}
			if keys.queue.Enabled() != tt.wantQueueEnabled {
				t.Errorf("queue enabled = %v, want %v", keys.queue.Enabled(), tt.wantQueueEnabled)
			}
			if got := keys.send.Help().Desc; got != tt.wantSendHelp {
				t.Errorf("send help = %q, want %q", got, tt.wantSendHelp)
			}
			if got := keys.interrupt.Help().Desc; got != tt.wantInterruptHelp {
				t.Errorf("interrupt help = %q, want %q", got, tt.wantInterruptHelp)
			}
		})
	}
}

func TestKeyMapShortHelpIncludesClearAndInterrupt(t *testing.T) {
	t.Parallel()

	keys := newKeyMap()
	binding := keys.help
	bindingKeys := binding.Keys()
	if len(bindingKeys) != 1 || bindingKeys[0] != "?" {
		t.Fatalf("help keys = %#v, want only question mark", bindingKeys)
	}
	if got := binding.Help(); got.Key != "?" || got.Desc != "shortcuts" {
		t.Errorf("help label = %#v, want question mark shortcuts", got)
	}

	current := newModel(nil, nil)
	idleHelp := current.footerKeys().ShortHelp()
	if len(idleHelp) != 2 || idleHelp[0].Help().Key != "?" || idleHelp[1].Help().Key != "Ctrl+c" {
		t.Errorf("idle short help = %#v, want question mark and control-c", idleHelp)
	}
	current.running = true
	shortHelp := current.footerKeys().ShortHelp()
	if len(shortHelp) != 3 ||
		shortHelp[0].Help().Key != "Esc" ||
		shortHelp[1].Help().Key != "Ctrl+c" ||
		shortHelp[2].Help().Key != "?" {
		t.Errorf(
			"running short help = %#v, want escape, control-c and question mark",
			shortHelp,
		)
	}
}

func TestKeyMapHistoryStaysHiddenAndDisablesWhileRunning(t *testing.T) {
	t.Parallel()

	current := newModel(nil, nil)
	idle := current.inputActionKeys()
	if !idle.historyUp.Enabled() || !idle.historyDown.Enabled() {
		t.Error("history bindings disabled while idle")
	}
	for _, running := range []bool{false, true} {
		current.running = running
		keys := current.inputActionKeys()
		if running && (keys.historyUp.Enabled() || keys.historyDown.Enabled()) {
			t.Error("history bindings enabled while running")
		}
		found := make(map[string]bool)
		for _, row := range current.footerKeys().FullHelp() {
			for _, binding := range row {
				if binding.Help().Key == "Up" || binding.Help().Key == "Down" {
					found[binding.Help().Key] = true
				}
			}
		}
		if len(found) != 0 {
			t.Errorf("help advertises basic history navigation: %v", found)
		}
	}
}
