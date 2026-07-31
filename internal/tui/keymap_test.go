package tui

import "testing"

func TestKeyMapForState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		running           bool
		wantSendEnabled   bool
		wantQuitEnabled   bool
		wantInterruptHelp string
	}{
		{
			name:              "idle",
			wantSendEnabled:   true,
			wantQuitEnabled:   true,
			wantInterruptHelp: "quit",
		},
		{
			name:              "running",
			running:           true,
			wantSendEnabled:   false,
			wantQuitEnabled:   false,
			wantInterruptHelp: "cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys := newKeyMap().forState(tt.running)
			if keys.send.Enabled() != tt.wantSendEnabled {
				t.Errorf("send enabled = %v, want %v", keys.send.Enabled(), tt.wantSendEnabled)
			}
			if keys.quit.Enabled() != tt.wantQuitEnabled {
				t.Errorf("quit enabled = %v, want %v", keys.quit.Enabled(), tt.wantQuitEnabled)
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
