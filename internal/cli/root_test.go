package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
)

func TestNewRootCommandRejectsMissingPrinter(t *testing.T) {
	t.Parallel()

	_, err := cli.NewRootCommand(cli.Dependencies{})
	if err == nil || !strings.Contains(err.Error(), "printer is required") {
		t.Fatalf("NewRootCommand() error = %v, want missing printer error", err)
	}
}

func TestNewRootCommandRejectsMissingInteractor(t *testing.T) {
	t.Parallel()

	_, err := cli.NewRootCommand(cli.Dependencies{Printer: &recordingPrinter{}})
	if err == nil || !strings.Contains(err.Error(), "interactor is required") {
		t.Fatalf("NewRootCommand() error = %v, want missing interactor error", err)
	}
}

func TestRootCommandRunsPrintMode(t *testing.T) {
	t.Parallel()

	printer := &recordingPrinter{response: "inspection complete\n"}
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    printer,
		Interactor: &recordingInteractor{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}

	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{
		"--workspace", "/workspace",
		"--print", "inspect this repository",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	wantRequest := cli.PrintRequest{
		Prompt:    "inspect this repository",
		Workspace: "/workspace",
	}
	if printer.request != wantRequest {
		t.Errorf("Print() request = %#v, want %#v", printer.request, wantRequest)
	}
	if got := output.String(); got != printer.response {
		t.Errorf("command output = %q, want %q", got, printer.response)
	}
}

func TestRootCommandRunsInteractiveMode(t *testing.T) {
	t.Parallel()

	interactor := &recordingInteractor{}
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: interactor,
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}

	input := strings.NewReader("input")
	output := new(bytes.Buffer)
	command.SetIn(input)
	command.SetOut(output)
	command.SetArgs([]string{"--workspace", "/workspace"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	if got := interactor.request.Workspace; got != "/workspace" {
		t.Errorf("Interactive() workspace = %q, want /workspace", got)
	}
	if interactor.request.Input != input {
		t.Error("Interactive() input does not match command input")
	}
	if interactor.request.Output != output {
		t.Error("Interactive() output does not match command output")
	}
}

func TestRootCommandRejectsUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "interactive positional argument",
			args: []string{"inspect"},
			want: "unknown command",
		},
		{
			name: "missing prompt",
			args: []string{"--print"},
			want: "accepts 1 arg(s), received 0",
		},
		{
			name: "blank prompt",
			args: []string{"--print", "  "},
			want: "prompt must not be blank",
		},
		{
			name: "blank workspace",
			args: []string{"--workspace", "  ", "--print", "inspect"},
			want: "workspace must not be blank",
		},
		{
			name: "unknown flag",
			args: []string{"--unknown"},
			want: "unknown flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command, err := cli.NewRootCommand(cli.Dependencies{
				Printer:    &recordingPrinter{},
				Interactor: &recordingInteractor{},
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

func TestRootCommandReturnsPrinterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("provider unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{err: wantErr},
		Interactor: &recordingInteractor{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetArgs([]string{"--print", "inspect"})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Errorf("ExitCode() = %d, want 1", got)
	}
}

func TestRootCommandReturnsInteractorError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("terminal unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{err: wantErr},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetArgs(nil)

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
}

func TestExitCodeCanceled(t *testing.T) {
	t.Parallel()

	if got := cli.ExitCode(fmt.Errorf("run: %w", context.Canceled)); got != 130 {
		t.Errorf("ExitCode() = %d, want 130", got)
	}
}

type recordingPrinter struct {
	request  cli.PrintRequest
	response string
	err      error
}

type recordingInteractor struct {
	request cli.InteractiveRequest
	err     error
}

func (i *recordingInteractor) Interactive(
	_ context.Context,
	request cli.InteractiveRequest,
) error {
	i.request = request
	return i.err
}

func (p *recordingPrinter) Print(
	_ context.Context,
	request cli.PrintRequest,
	output io.Writer,
) error {
	p.request = request
	if p.err != nil {
		return p.err
	}
	_, err := io.WriteString(output, p.response)
	return err
}
