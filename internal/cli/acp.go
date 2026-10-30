package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// ACPRequest contains one Agent Client Protocol server invocation over stdio.
type ACPRequest struct {
	Workspace            string
	Session              string
	Input                io.Reader
	Output               io.Writer
	ProjectTrustOverride *bool
	// Yolo upgrades Guard Decision ask to allow. Deny is unchanged.
	Yolo bool
}

// ACPServer serves one ACP client until it disconnects.
type ACPServer interface {
	ServeACP(ctx context.Context, request ACPRequest) error
}

func newACPCommand(server ACPServer) *cobra.Command {
	options := rootOptions{workspace: "."}
	command := &cobra.Command{
		Use:   "acp",
		Short: "Serve the Agent Client Protocol over stdio",
		Args: func(command *cobra.Command, args []string) error {
			if options.approve && options.noApprove {
				return newUsageError(errors.New("--approve and --no-approve are mutually exclusive"))
			}
			if strings.TrimSpace(options.workspace) == "" {
				return newUsageError(errors.New("workspace must not be blank"))
			}
			return newUsageError(cobra.NoArgs(command, args))
		},
		RunE: func(command *cobra.Command, _ []string) error {
			request := ACPRequest{
				Workspace:            options.workspace,
				Session:              options.session,
				Input:                command.InOrStdin(),
				Output:               command.OutOrStdout(),
				ProjectTrustOverride: projectTrustOverride(options.approve, options.noApprove),
				Yolo:                 options.yolo,
			}
			if err := server.ServeACP(command.Context(), request); err != nil {
				return fmt.Errorf("acp: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&options.workspace, "workspace", options.workspace, "working directory for agent tools")
	command.Flags().StringVar(&options.session, "session", "", "session JSONL file to create or resume")
	command.Flags().BoolVarP(&options.approve, "approve", "a", false, "trust project-local resources for this run")
	command.Flags().BoolVar(&options.noApprove, "no-approve", false, "ignore project-local resources for this run")
	command.Flags().BoolVar(&options.yolo, "yolo", false, "automatically allow tool calls that would otherwise ask, except changes to AICE configuration; dangerous")
	return command
}
