// Package cli defines AICE's command-line surface and exit behavior.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// PrintRequest contains one non-interactive AICE invocation.
type PrintRequest struct {
	Prompt    string
	Workspace string
}

// Printer runs one non-interactive agent request.
type Printer interface {
	Print(ctx context.Context, request PrintRequest, output io.Writer) error
}

// Dependencies contains the behavior invoked by CLI commands.
type Dependencies struct {
	Printer Printer
}

// NewRootCommand builds a fresh AICE command tree.
func NewRootCommand(dependencies Dependencies) (*cobra.Command, error) {
	if dependencies.Printer == nil {
		return nil, fmt.Errorf("cli: printer is required")
	}

	options := rootOptions{workspace: "."}
	command := &cobra.Command{
		Use:           "aice --print <prompt>",
		Short:         "A small, provider-neutral coding agent",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(command *cobra.Command, args []string) error {
			if !options.print {
				return newUsageError(errors.New(
					"interactive mode is not available yet; use --print",
				))
			}
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return newUsageError(err)
			}
			if strings.TrimSpace(args[0]) == "" {
				return newUsageError(errors.New("prompt must not be blank"))
			}
			if strings.TrimSpace(options.workspace) == "" {
				return newUsageError(errors.New("workspace must not be blank"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			request := PrintRequest{
				Prompt:    args[0],
				Workspace: options.workspace,
			}
			if err := dependencies.Printer.Print(
				command.Context(),
				request,
				command.OutOrStdout(),
			); err != nil {
				return fmt.Errorf("print response: %w", err)
			}
			return nil
		},
	}
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newUsageError(err)
	})
	command.Flags().BoolVarP(
		&options.print,
		"print",
		"p",
		false,
		"print one response and exit",
	)
	command.Flags().StringVar(
		&options.workspace,
		"workspace",
		options.workspace,
		"workspace root exposed to agent tools",
	)

	return command, nil
}

// ExitCode maps a command failure to a process exit status.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var usageError *UsageError
	if errors.As(err, &usageError) {
		return 2
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

type rootOptions struct {
	print     bool
	workspace string
}

// UsageError marks invalid command-line input.
type UsageError struct {
	err error
}

func newUsageError(err error) error {
	if err == nil {
		return nil
	}
	var usageError *UsageError
	if errors.As(err, &usageError) {
		return err
	}
	return &UsageError{err: err}
}

// Error returns the underlying command-line error.
func (e *UsageError) Error() string {
	return e.err.Error()
}

// Unwrap exposes the original command-line error.
func (e *UsageError) Unwrap() error {
	return e.err
}
