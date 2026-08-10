package tui

import "testing"

func TestKeyMapForState(t *testing.T) {
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
			wantInterruptHelp: "quit",
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

			keys := newKeyMap().forState(tt.running, tt.acceptsDelivery)
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

func TestKeyMapShortHelpUsesOnlyQuestionMarkAndInterrupt(t *testing.T) {
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

	shortHelp := keys.ShortHelp()
	if len(shortHelp) != 2 ||
		shortHelp[0].Help().Key != "?" ||
		shortHelp[1].Help().Key != "ctrl+C" {
		t.Errorf(
			"short help = %#v, want only question mark and control-c",
			shortHelp,
		)
	}
}

func TestKeyMapHistoryShowsInFullHelpAndDisablesWhileRunning(t *testing.T) {
	t.Parallel()

	keys := newKeyMap()
	fullHelp := keys.FullHelp()
	found := false
	for _, row := range fullHelp {
		for _, binding := range row {
			if binding.Help().Key == "up/down" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("full help = %#v, want up/down history binding", fullHelp)
	}

	idle := newKeyMap().forState(false, false)
	if !idle.history.Enabled() {
		t.Error("history binding disabled while idle")
	}
	running := newKeyMap().forState(true, false)
	if running.history.Enabled() {
		t.Error("history binding enabled while running")
	}
}
