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

func TestRootCommandRunsPrintMode(t *testing.T) {
	t.Parallel()

	printer := &recordingPrinter{response: "inspection complete\n"}
	command, err := cli.NewRootCommand(cli.Dependencies{Printer: printer})
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

func TestRootCommandRejectsUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "interactive mode",
			args: []string{"inspect"},
			want: "use --print",
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
				Printer: &recordingPrinter{},
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
		Printer: &recordingPrinter{err: wantErr},
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
