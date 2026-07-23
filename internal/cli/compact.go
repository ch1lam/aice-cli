package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// CompactRequest identifies one existing Session to compact.
type CompactRequest struct {
	Workspace string
	Session   string
}

// Compactor derives and persists one append-only Session checkpoint.
type Compactor interface {
	Compact(ctx context.Context, request CompactRequest, output io.Writer) error
}

func newCompactCommand(compactor Compactor) *cobra.Command {
	options := compactOptions{workspace: "."}
	command := &cobra.Command{
		Use:   "compact",
		Short: "Compact an existing Session without rewriting its history",
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return newUsageError(err)
			}
			if strings.TrimSpace(options.workspace) == "" {
				return newUsageError(errors.New("workspace must not be blank"))
			}
			if !command.Flags().Changed("session") {
				return newUsageError(errors.New("session is required"))
			}
			if strings.TrimSpace(options.session) == "" {
				return newUsageError(errors.New("session must not be blank"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			request := CompactRequest{
				Workspace: options.workspace,
				Session:   options.session,
			}
			if err := compactor.Compact(
				command.Context(),
				request,
				command.OutOrStdout(),
			); err != nil {
				return fmt.Errorf("compact session: %w", err)
			}
			return nil
		},
	}
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newUsageError(err)
	})
	command.Flags().StringVar(
		&options.workspace,
		"workspace",
		options.workspace,
		"working directory recorded by the Session",
	)
	command.Flags().StringVar(
		&options.session,
		"session",
		"",
		"existing Session JSONL file to compact",
	)
	return command
}

type compactOptions struct {
	workspace string
	session   string
}
