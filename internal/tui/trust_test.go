package tui

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/trust"
)

func trustChoicesForTest() []trust.Choice {
	return []trust.Choice{
		{Label: "Trust", Decision: trust.DecisionTrusted},
		{Label: "Trust (this session only)", Decision: trust.DecisionTrusted},
		{Label: "Do not trust", Decision: trust.DecisionUntrusted},
	}
}

func TestRunTrustPromptRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     context.Context
		options TrustPromptOptions
		want    string
	}{
		{name: "nil context", want: "context is required"},
		{
			name: "nil input",
			ctx:  t.Context(),
			options: TrustPromptOptions{
				Output: io.Discard,
			},
			want: "input is required",
		},
		{
			name: "nil output",
			ctx:  t.Context(),
			options: TrustPromptOptions{
				Input: strings.NewReader(""),
			},
			want: "output is required",
		},
		{
			name: "blank cwd",
			ctx:  t.Context(),
			options: TrustPromptOptions{
				Input:   strings.NewReader(""),
				Output:  io.Discard,
				Choices: trustChoicesForTest(),
			},
			want: "working directory is required",
		},
		{
			name: "no choices",
			ctx:  t.Context(),
			options: TrustPromptOptions{
				Input:  strings.NewReader(""),
				Output: io.Discard,
				CWD:    "/workspace",
			},
			want: "trust choices are required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := RunTrustPrompt(tt.ctx, tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunTrustPrompt() error = %v, want text %q", err, tt.want)
			}
		})
	}
}

func TestTrustPromptModelSelectsChoice(t *testing.T) {
	t.Parallel()

	model := trustPromptModel{
		cwd:     "/workspace",
		choices: trustChoicesForTest(),
	}
	// Arrow down twice, then enter selects the third choice.
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: tea.KeyDown},
		{Code: tea.KeyEnter},
	} {
		updated, _ := model.Update(key)
		var ok bool
		model, ok = updated.(trustPromptModel)
		if !ok {
			t.Fatalf("Update() returned %T, want trustPromptModel", updated)
		}
	}
	if !model.done {
		t.Fatal("model did not finish after enter")
	}
	if model.choice.Label != "Do not trust" ||
		model.choice.Decision != trust.DecisionUntrusted {
		t.Errorf("choice = %#v, want Do not trust", model.choice)
	}
}

func TestTrustPromptModelClampsSelection(t *testing.T) {
	t.Parallel()

	model := trustPromptModel{
		cwd:     "/workspace",
		choices: trustChoicesForTest(),
	}
	// Move up past the first option and down past the last; the selection
	// must stay in range.
	model = updateTrustModel(t, model, tea.KeyPressMsg{Code: tea.KeyUp})
	if model.selected != 0 {
		t.Errorf("selected = %d, want 0", model.selected)
	}
	for range 10 {
		model = updateTrustModel(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.selected != len(model.choices)-1 {
		t.Errorf("selected = %d, want %d", model.selected, len(model.choices)-1)
	}
}

func TestTrustPromptModelEscapestoTsSessionOnlyDeny(t *testing.T) {
	t.Parallel()

	model := trustPromptModel{
		cwd:     "/workspace",
		choices: trustChoicesForTest(),
	}
	model = updateTrustModel(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.done {
		t.Fatal("model did not finish after escape")
	}
	if model.choice.Decision != trust.DecisionUntrusted {
		t.Errorf("choice = %#v, want untrusted", model.choice)
	}
	if len(model.choice.Updates) != 0 {
		t.Errorf("choice updates = %#v, want empty", model.choice.Updates)
	}
}

func TestTrustPromptModelViewListsChoices(t *testing.T) {
	t.Parallel()

	model := trustPromptModel{
		cwd:     "/workspace",
		choices: trustChoicesForTest(),
	}
	view := model.View().Content
	for _, want := range []string{
		"Trust project folder?",
		"/workspace",
		"Trust",
		"Do not trust",
		"Enter to select",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view = %q, want %q", view, want)
		}
	}
}

func updateTrustModel(
	t *testing.T,
	model trustPromptModel,
	message tea.KeyPressMsg,
) trustPromptModel {
	t.Helper()
	updated, _ := model.Update(message)
	next, ok := updated.(trustPromptModel)
	if !ok {
		t.Fatalf("Update() returned %T, want trustPromptModel", updated)
	}
	return next
}
