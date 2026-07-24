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

func TestCompactCommandRunsCompactor(t *testing.T) {
	t.Parallel()

	compactor := &compactRecorder{response: "session compacted\n"}
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{},
		Compactor:  compactor,
		Navigator:  &recordingNavigator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}

	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{
		"compact",
		"--workspace", "/workspace",
		"--session", "/sessions/conversation.jsonl",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	want := cli.CompactRequest{
		Workspace: "/workspace",
		Session:   "/sessions/conversation.jsonl",
	}
	if compactor.request != want {
		t.Errorf("Compact() request = %#v, want %#v", compactor.request, want)
	}
	if got := output.String(); got != compactor.response {
		t.Errorf("command output = %q, want %q", got, compactor.response)
	}
}

func TestCompactCommandRejectsUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "positional argument",
			args: []string{"compact", "conversation.jsonl"},
			want: "unknown command",
		},
		{
			name: "missing session",
			args: []string{"compact"},
			want: "session is required",
		},
		{
			name: "blank session",
			args: []string{"compact", "--session", "  "},
			want: "session must not be blank",
		},
		{
			name: "blank workspace",
			args: []string{
				"compact",
				"--workspace", "  ",
				"--session", "conversation.jsonl",
			},
			want: "workspace must not be blank",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command, err := cli.NewRootCommand(cli.Dependencies{
				Printer:    &recordingPrinter{},
				Interactor: &recordingInteractor{},
				Compactor:  &compactRecorder{},
				Navigator:  &recordingNavigator{},
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

func TestCompactCommandReturnsCompactorError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("summary unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{},
		Compactor:  &compactRecorder{err: wantErr},
		Navigator:  &recordingNavigator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetArgs([]string{
		"compact",
		"--session", "conversation.jsonl",
	})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Errorf("ExitCode() = %d, want 1", got)
	}
}

type compactRecorder struct {
	request  cli.CompactRequest
	response string
	err      error
}

func (c *compactRecorder) Compact(
	_ context.Context,
	request cli.CompactRequest,
	output io.Writer,
) error {
	c.request = request
	if c.err != nil {
		return c.err
	}
	_, err := io.WriteString(output, c.response)
	return err
}
