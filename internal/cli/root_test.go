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

func TestNewRootCommandRejectsMissingCompactor(t *testing.T) {
	t.Parallel()

	_, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{},
	})
	if err == nil || !strings.Contains(err.Error(), "compactor is required") {
		t.Fatalf("NewRootCommand() error = %v, want missing compactor error", err)
	}
}

func TestNewRootCommandRejectsMissingSessionNavigator(t *testing.T) {
	t.Parallel()

	_, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{},
		Compactor:  &recordingCompactor{},
	})
	if err == nil || !strings.Contains(err.Error(), "session navigator is required") {
		t.Fatalf("NewRootCommand() error = %v, want missing navigator error", err)
	}
}

func TestNewRootCommandRejectsMissingConfigurator(t *testing.T) {
	t.Parallel()

	_, err := cli.NewRootCommand(cli.Dependencies{
		Printer:    &recordingPrinter{},
		Interactor: &recordingInteractor{},
		Compactor:  &recordingCompactor{},
		Navigator:  &recordingNavigator{},
	})
	if err == nil || !strings.Contains(err.Error(), "configurator is required") {
		t.Fatalf("NewRootCommand() error = %v, want missing configurator error", err)
	}
}

func TestRootCommandRunsPrintMode(t *testing.T) {
	t.Parallel()

	printer := &recordingPrinter{response: "inspection complete\n"}
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      printer,
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}

	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{
		"--workspace", "/workspace",
		"--session", "/sessions/inspection.jsonl",
		"--print", "inspect this repository",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	wantRequest := cli.PrintRequest{
		Prompt:    "inspect this repository",
		Workspace: "/workspace",
		Session:   "/sessions/inspection.jsonl",
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
		Printer:      &recordingPrinter{},
		Interactor:   interactor,
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}

	input := strings.NewReader("input")
	output := new(bytes.Buffer)
	command.SetIn(input)
	command.SetOut(output)
	command.SetArgs([]string{
		"--workspace", "/workspace",
		"--session", "/sessions/interactive.jsonl",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	if got := interactor.request.Workspace; got != "/workspace" {
		t.Errorf("Interactive() workspace = %q, want /workspace", got)
	}
	if got := interactor.request.Session; got != "/sessions/interactive.jsonl" {
		t.Errorf(
			"Interactive() session = %q, want /sessions/interactive.jsonl",
			got,
		)
	}
	if interactor.request.Input != input {
		t.Error("Interactive() input does not match command input")
	}
	if interactor.request.Output != output {
		t.Error("Interactive() output does not match command output")
	}
}

func TestRootCommandDescribesWorkspaceAsWorkingDirectory(t *testing.T) {
	t.Parallel()

	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "working directory for agent tools") {
		t.Fatalf("help = %q, want working-directory description", help)
	}
	if !strings.Contains(help, "session JSONL file to create or resume") {
		t.Fatalf("help = %q, want session-file description", help)
	}
	if strings.Contains(help, "root exposed") {
		t.Fatalf("help = %q, still describes an access boundary", help)
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
			name: "blank session",
			args: []string{"--session", "  ", "--print", "inspect"},
			want: "session must not be blank",
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
				Printer:      &recordingPrinter{},
				Interactor:   &recordingInteractor{},
				Compactor:    &recordingCompactor{},
				Navigator:    &recordingNavigator{},
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

func TestRootCommandReturnsPrinterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("provider unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{err: wantErr},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
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
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{err: wantErr},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
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

type recordingCompactor struct{}

type recordingNavigator struct{}

type recordingConfigurator struct{}

func (*recordingCompactor) Compact(
	context.Context,
	cli.CompactRequest,
	io.Writer,
) error {
	return nil
}

func (*recordingNavigator) SessionTree(
	context.Context,
	cli.SessionTreeRequest,
	io.Writer,
) error {
	return nil
}

func (*recordingNavigator) CheckoutSession(
	context.Context,
	cli.SessionCheckoutRequest,
	io.Writer,
) error {
	return nil
}

func (*recordingConfigurator) SaveAPIKey(
	context.Context,
	cli.APIKeyRequest,
) (string, error) {
	return "/global/auth.json", nil
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
