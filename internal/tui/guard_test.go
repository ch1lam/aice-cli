package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestGuardViewRendersCommandCard(t *testing.T) {
	t.Parallel()

	current := newGuardTestModel(t, interaction.GuardRequest{
		ToolName:  "bash",
		Reason:    "Dangerous command requires confirmation.",
		RuleID:    "permissionGate.dangerous",
		Command:   "rm -rf /tmp/workspace",
		Highlight: "rm -rf",
		Options:   guardTestOptions(),
	})
	if current.composerInputEnabled() || current.input.Focused() {
		t.Fatal("composer should stay disabled while a guard prompt is open")
	}

	view := guardViewText(current)
	for _, want := range []string{
		"Run this command?",
		"$ rm -rf /tmp/workspace",
		"rm -rf",
		"1. Allow once",
		"2. Allow this exact command for this run",
		`3. Allow "ls …" commands for this run`,
		"current run only",
		"4. Deny",
		"↑/↓ select · 1-9/enter confirm · y first · n/esc deny",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestGuardViewTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  interaction.GuardRequest
		want string
	}{
		{
			name: "command",
			req:  interaction.GuardRequest{Command: "ls"},
			want: "Run this command?",
		},
		{
			name: "path",
			req:  interaction.GuardRequest{Path: "/tmp/secret.env"},
			want: "Allow access outside the workspace?",
		},
		{
			name: "unknown tool",
			req:  interaction.GuardRequest{ToolName: "mystery"},
			want: `Allow tool "mystery"?`,
		},
		{
			name: "empty action",
			req:  interaction.GuardRequest{},
			want: "Allow this action?",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := tt.req
			req.Options = guardTestOptions()
			view := guardViewText(newGuardTestModel(t, req))
			if !strings.Contains(view, tt.want) {
				t.Errorf("view is missing title %q:\n%s", tt.want, view)
			}
			if tt.name != "command" && strings.Contains(view, "Run this command?") {
				t.Errorf("view used the command title unexpectedly:\n%s", view)
			}
		})
	}
}

func TestGuardViewShortensHomePath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}
	current := newGuardTestModel(t, interaction.GuardRequest{
		Path:    filepath.Join(home, "outside", "secret.env"),
		Options: guardTestOptions(),
	})
	view := guardViewText(current)
	want := "~/outside/secret.env"
	if !strings.Contains(view, want) {
		t.Errorf("view = %q, want shortened path %q", view, want)
	}
	if strings.Contains(view, `~\outside\secret.env`) {
		t.Errorf("view used native separators, want %q:\n%s", want, view)
	}
}

func TestGuardDisplayPath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "nested path under home uses forward slashes",
			path: filepath.Join(home, "outside", "secret.env"),
			want: "~/outside/secret.env",
		},
		{
			name: "home itself",
			path: home,
			want: "~",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := guardDisplayPath(tt.path)
			if got != tt.want {
				t.Errorf("guardDisplayPath() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, `\`) {
				t.Errorf("guardDisplayPath() uses backslash: %q", got)
			}
		})
	}

	if runtime.GOOS == "windows" {
		t.Run("case fold home prefix", func(t *testing.T) {
			t.Parallel()
			flipped := flipASCIICase(home)
			got := guardDisplayPath(filepath.Join(flipped, "outside", "secret.env"))
			if got != "~/outside/secret.env" {
				t.Errorf("guardDisplayPath(case-fold) = %q, want ~/outside/secret.env", got)
			}
		})
	}
}

func flipASCIICase(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + 32
		case c >= 'a' && c <= 'z':
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestGuardKeysConfirmOptions(t *testing.T) {
	t.Parallel()

	t.Run("digit confirms matching option", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: '2', Text: "2"})
		assertGuardReply(t, reply, interaction.GuardReply{OptionID: "allow-always"})
		if current.guardPending != nil {
			t.Fatal("guard prompt remained after confirming")
		}
		if !current.input.Focused() {
			t.Fatal("composer was not restored after confirming")
		}
	})

	t.Run("y confirms first option", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: 'y', Text: "y"})
		assertGuardReply(t, reply, interaction.GuardReply{OptionID: "allow-once"})
		if current.guardPending != nil {
			t.Fatal("guard prompt remained after confirming")
		}
	})

	t.Run("arrows clamp without wrapping", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
		if current.guardSelection != 0 {
			t.Errorf("selection = %d, want 0", current.guardSelection)
		}
		for range 10 {
			current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
		}
		if current.guardSelection != len(guardTestOptions())-1 {
			t.Errorf(
				"selection = %d, want %d",
				current.guardSelection,
				len(guardTestOptions())-1,
			)
		}
		assertNoGuardReply(t, reply)
	})
}

func TestGuardDenyFeedback(t *testing.T) {
	t.Parallel()

	t.Run("n then typed note sends feedback", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: 'n', Text: "n"})
		if !current.guardFeedback {
			t.Fatal("n did not enter deny feedback")
		}
		view := guardViewText(current)
		if !strings.Contains(view, "Tell the agent what to do instead (optional):") {
			t.Fatalf("feedback view is missing the prompt:\n%s", view)
		}
		assertNoGuardReply(t, reply)

		current = typeGuardText(t, current, "use rg instead")
		current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
		assertGuardReply(t, reply, interaction.GuardReply{
			OptionID: "deny",
			Feedback: "use rg instead",
		})
		if current.guardPending != nil || current.guardFeedback {
			t.Fatal("guard prompt remained after sending deny feedback")
		}
	})

	t.Run("escape returns to the option list", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: 'n', Text: "n"})
		current = typeGuardText(t, current, "scratch")
		current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyEscape})
		if current.guardFeedback {
			t.Fatal("escape did not leave deny feedback")
		}
		if current.guardPending == nil {
			t.Fatal("escape dismissed the guard prompt")
		}
		view := guardViewText(current)
		if strings.Contains(view, "Tell the agent what to do instead (optional):") {
			t.Fatalf("option list still shows the feedback prompt:\n%s", view)
		}
		assertNoGuardReply(t, reply)
	})

	t.Run("enter sends empty feedback", func(t *testing.T) {
		t.Parallel()

		reply := make(chan interaction.GuardReply, 1)
		current := newGuardTestModel(t, interaction.GuardRequest{
			Command: "ls",
			Options: guardTestOptions(),
			Reply:   reply,
		})
		current = updateModel(t, current, tea.KeyPressMsg{Code: 'n', Text: "n"})
		current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
		assertGuardReply(t, reply, interaction.GuardReply{OptionID: "deny"})
	})
}

func guardTestOptions() []interaction.GuardOption {
	return []interaction.GuardOption{
		{ID: "allow-once", Label: "Allow once"},
		{ID: "allow-always", Label: "Allow this exact command for this run"},
		{
			ID:     "allow-session",
			Label:  `Allow "ls …" commands for this run`,
			Detail: "current run only",
		},
		{ID: "deny", Label: "Deny", Deny: true},
	}
}

func newGuardTestModel(t *testing.T, req interaction.GuardRequest) model {
	t.Helper()

	if req.Reply == nil {
		req.Reply = make(chan interaction.GuardReply, 1)
	}
	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	copied := req
	return updateModel(t, current, guardRequestMsg{req: &copied})
}

func typeGuardText(t *testing.T, current model, text string) model {
	t.Helper()

	for _, character := range text {
		current = updateModel(t, current, tea.KeyPressMsg{
			Code: character,
			Text: string(character),
		})
	}
	return current
}

func guardViewText(current model) string {
	return ansi.Strip(current.View().Content)
}

func assertGuardReply(
	t *testing.T,
	replies <-chan interaction.GuardReply,
	want interaction.GuardReply,
) {
	t.Helper()

	select {
	case got := <-replies:
		if got != want {
			t.Errorf("GuardReply = %#v, want %#v", got, want)
		}
	default:
		t.Fatal("no GuardReply received")
	}
}

func assertNoGuardReply(t *testing.T, replies <-chan interaction.GuardReply) {
	t.Helper()

	select {
	case got := <-replies:
		t.Fatalf("unexpected GuardReply: %#v", got)
	default:
	}
}
