package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// UpdateRequest describes one aice update invocation.
type UpdateRequest struct {
	Check bool
	Force bool
}

// Updater installs a newer AICE release over the running executable.
type Updater interface {
	Update(ctx context.Context, request UpdateRequest, output io.Writer) error
}

func newUpdateCommand(updater Updater) *cobra.Command {
	options := updateOptions{}
	command := &cobra.Command{
		Use:   "update",
		Short: "Update AICE to the latest release",
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return newUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			request := UpdateRequest{
				Check: options.check,
				Force: options.force,
			}
			if err := updater.Update(
				command.Context(),
				request,
				command.OutOrStdout(),
			); err != nil {
				return fmt.Errorf("update aice: %w", err)
			}
			return nil
		},
	}
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newUsageError(err)
	})
	command.Flags().BoolVar(
		&options.check,
		"check",
		false,
		"report whether a newer release is available without installing",
	)
	command.Flags().BoolVar(
		&options.force,
		"force",
		false,
		"install the latest release even when the current version cannot be compared",
	)
	return command
}

type updateOptions struct {
	check bool
	force bool
}
