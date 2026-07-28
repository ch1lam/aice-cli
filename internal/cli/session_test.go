package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
)

func TestSessionCommandsRunNavigator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantTree     cli.SessionTreeRequest
		wantCheckout cli.SessionCheckoutRequest
	}{
		{
			name: "tree",
			args: []string{
				"session", "tree",
				"--workspace", "/workspace",
				"--session", "/sessions/conversation.jsonl",
			},
			wantTree: cli.SessionTreeRequest{
				Workspace: "/workspace",
				Session:   "/sessions/conversation.jsonl",
			},
		},
		{
			name: "checkout",
			args: []string{
				"session", "checkout",
				"--workspace", "/workspace",
				"--session", "/sessions/conversation.jsonl",
				"--entry", "turn-1",
			},
			wantCheckout: cli.SessionCheckoutRequest{
				Workspace: "/workspace",
				Session:   "/sessions/conversation.jsonl",
				Entry:     "turn-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			navigator := &sessionRecorder{response: tt.name + "\n"}
			command, err := cli.NewRootCommand(cli.Dependencies{
				Printer:      &recordingPrinter{},
				Interactor:   &recordingInteractor{},
				Compactor:    &recordingCompactor{},
				Navigator:    navigator,
				Configurator: &recordingConfigurator{},
			})
			if err != nil {
				t.Fatalf("NewRootCommand() error = %v", err)
			}
			output := new(bytes.Buffer)
			command.SetOut(output)
			command.SetArgs(tt.args)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
			if navigator.treeRequest != tt.wantTree {
				t.Errorf(
					"SessionTree() request = %#v, want %#v",
					navigator.treeRequest,
					tt.wantTree,
				)
			}
			if navigator.checkoutRequest != tt.wantCheckout {
				t.Errorf(
					"CheckoutSession() request = %#v, want %#v",
					navigator.checkoutRequest,
					tt.wantCheckout,
				)
			}
			if got := output.String(); got != navigator.response {
				t.Errorf("output = %q, want %q", got, navigator.response)
			}
		})
	}
}

func TestSessionCommandsRejectUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "tree missing session",
			args: []string{"session", "tree"},
			want: "session must not be blank",
		},
		{
			name: "checkout missing entry",
			args: []string{
				"session", "checkout",
				"--session", "conversation.jsonl",
			},
			want: "entry is required",
		},
		{
			name: "checkout blank entry",
			args: []string{
				"session", "checkout",
				"--session", "conversation.jsonl",
				"--entry", "  ",
			},
			want: "entry must not be blank",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command, err := cli.NewRootCommand(cli.Dependencies{
				Printer:      &recordingPrinter{},
				Interactor:   &recordingInteractor{},
				Compactor:    &recordingCompactor{},
				Navigator:    &sessionRecorder{},
				Configurator: &recordingConfigurator{},
			})
			if err != nil {
				t.Fatalf("NewRootCommand() error = %v", err)
			}
			command.SetArgs(tt.args)
			err = command.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ExecuteContext() error = %v, want text %q", err, tt.want)
			}
			if got := cli.ExitCode(err); got != 2 {
				t.Errorf("ExitCode() = %d, want 2", got)
			}
		})
	}
}

func TestSessionCommandReturnsNavigatorError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("session unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &sessionRecorder{err: wantErr},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetArgs([]string{
		"session", "tree",
		"--session", "conversation.jsonl",
	})
	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
}

type sessionRecorder struct {
	treeRequest     cli.SessionTreeRequest
	checkoutRequest cli.SessionCheckoutRequest
	response        string
	err             error
}

func (s *sessionRecorder) SessionTree(
	_ context.Context,
	request cli.SessionTreeRequest,
	output io.Writer,
) error {
	s.treeRequest = request
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(output, s.response)
	return err
}

func (s *sessionRecorder) CheckoutSession(
	_ context.Context,
	request cli.SessionCheckoutRequest,
	output io.Writer,
) error {
	s.checkoutRequest = request
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(output, s.response)
	return err
}
