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
	Session   string
	// OutputFormat selects text or NDJSON output. An empty value is treated as text.
	OutputFormat         string
	ProjectTrustOverride *bool
	// Yolo upgrades Guard Decision ask to allow. Deny is unchanged.
	Yolo bool
}

// InteractiveRequest contains one interactive AICE session invocation.
type InteractiveRequest struct {
	Workspace            string
	Session              string
	Input                io.Reader
	Output               io.Writer
	ProjectTrustOverride *bool
	// Yolo upgrades Guard Decision ask to allow. Deny is unchanged.
	Yolo bool
}

// Printer streams one non-interactive response to output and operational
// progress to diagnostics.
type Printer interface {
	Print(
		ctx context.Context,
		request PrintRequest,
		output io.Writer,
		diagnostics io.Writer,
	) error
}

// Interactor runs one interactive terminal session.
type Interactor interface {
	Interactive(ctx context.Context, request InteractiveRequest) error
}

// Dependencies contains the behavior invoked by CLI commands. Updater is
// optional: when set it registers the update command.
type Dependencies struct {
	Printer      Printer
	Interactor   Interactor
	Compactor    Compactor
	Navigator    SessionNavigator
	Configurator Configurator
	Updater      Updater
}

// NewRootCommand builds a fresh AICE command tree.
func NewRootCommand(dependencies Dependencies) (*cobra.Command, error) {
	if dependencies.Printer == nil {
		return nil, fmt.Errorf("cli: printer is required")
	}
	if dependencies.Interactor == nil {
		return nil, fmt.Errorf("cli: interactor is required")
	}
	if dependencies.Compactor == nil {
		return nil, fmt.Errorf("cli: compactor is required")
	}
	if dependencies.Navigator == nil {
		return nil, fmt.Errorf("cli: session navigator is required")
	}
	if dependencies.Configurator == nil {
		return nil, fmt.Errorf("cli: configurator is required")
	}

	options := rootOptions{workspace: ".", outputFormat: "text"}
	command := &cobra.Command{
		Use:           "aice [--print <prompt>]",
		Short:         "A small, provider-neutral coding agent",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(command *cobra.Command, args []string) error {
			if options.approve && options.noApprove {
				return newUsageError(errors.New(
					"--approve and --no-approve are mutually exclusive",
				))
			}
			if strings.TrimSpace(options.workspace) == "" {
				return newUsageError(errors.New("workspace must not be blank"))
			}
			if command.Flags().Changed("session") &&
				strings.TrimSpace(options.session) == "" {
				return newUsageError(errors.New("session must not be blank"))
			}
			if command.Flags().Changed("output-format") && !options.print {
				return newUsageError(errors.New(
					"--output-format is only valid with --print",
				))
			}
			if options.outputFormat != "text" && options.outputFormat != "json" {
				return newUsageError(fmt.Errorf(
					"unsupported output format %q; use text or json",
					options.outputFormat,
				))
			}
			if !options.print {
				if err := cobra.NoArgs(command, args); err != nil {
					return newUsageError(err)
				}
				return nil
			}
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return newUsageError(err)
			}
			if strings.TrimSpace(args[0]) == "" {
				return newUsageError(errors.New("prompt must not be blank"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			override := projectTrustOverride(options.approve, options.noApprove)
			if !options.print {
				request := InteractiveRequest{
					Workspace:            options.workspace,
					Session:              options.session,
					Input:                command.InOrStdin(),
					Output:               command.OutOrStdout(),
					ProjectTrustOverride: override,
					Yolo:                 options.yolo,
				}
				if err := dependencies.Interactor.Interactive(
					command.Context(),
					request,
				); err != nil {
					return fmt.Errorf("interactive session: %w", err)
				}
				return nil
			}

			request := PrintRequest{
				Prompt:               args[0],
				Workspace:            options.workspace,
				Session:              options.session,
				OutputFormat:         options.outputFormat,
				ProjectTrustOverride: override,
				Yolo:                 options.yolo,
			}
			if err := dependencies.Printer.Print(
				command.Context(),
				request,
				command.OutOrStdout(),
				command.ErrOrStderr(),
			); err != nil {
				return fmt.Errorf("print response: %w", err)
			}
			return nil
		},
	}
	command.Version = Version
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
		"working directory for agent tools",
	)
	command.Flags().StringVar(
		&options.session,
		"session",
		"",
		"session JSONL file to create or resume",
	)
	command.Flags().StringVar(
		&options.outputFormat,
		"output-format",
		options.outputFormat,
		"print output format: text or json",
	)
	command.Flags().BoolVarP(
		&options.approve,
		"approve",
		"a",
		false,
		"trust project-local resources for this run",
	)
	command.Flags().BoolVar(
		&options.noApprove,
		"no-approve",
		false,
		"ignore project-local resources for this run",
	)
	command.Flags().BoolVar(
		&options.yolo,
		"yolo",
		false,
		"automatically allow tool calls that would otherwise ask; for isolated containers/CI; dangerous",
	)
	command.AddCommand(newCompactCommand(dependencies.Compactor))
	command.AddCommand(newSessionCommand(dependencies.Navigator))
	command.AddCommand(newConfigCommand(dependencies.Configurator))
	if dependencies.Updater != nil {
		command.AddCommand(newUpdateCommand(dependencies.Updater))
	}

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
	print        bool
	workspace    string
	session      string
	outputFormat string
	approve      bool
	noApprove    bool
	yolo         bool
}

// projectTrustOverride combines the mutually exclusive trust flags into a
// tri-state override: nil when neither flag is set.
func projectTrustOverride(approve, noApprove bool) *bool {
	if approve {
		value := true
		return &value
	}
	if noApprove {
		value := false
		return &value
	}
	return nil
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
