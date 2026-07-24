package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// SessionTreeRequest identifies one Session tree to inspect.
type SessionTreeRequest struct {
	Workspace string
	Session   string
}

// SessionCheckoutRequest identifies the node that should become active.
type SessionCheckoutRequest struct {
	Workspace string
	Session   string
	Entry     string
}

// SessionNavigator exposes tree inspection and append-only branch movement.
type SessionNavigator interface {
	SessionTree(
		ctx context.Context,
		request SessionTreeRequest,
		output io.Writer,
	) error
	CheckoutSession(
		ctx context.Context,
		request SessionCheckoutRequest,
		output io.Writer,
	) error
}

func newSessionCommand(navigator SessionNavigator) *cobra.Command {
	command := &cobra.Command{
		Use:   "session",
		Short: "Inspect and navigate append-only Session trees",
		Args: func(command *cobra.Command, args []string) error {
			return newUsageError(cobra.NoArgs(command, args))
		},
	}
	command.AddCommand(
		newSessionTreeCommand(navigator),
		newSessionCheckoutCommand(navigator),
	)
	return command
}

func newSessionTreeCommand(navigator SessionNavigator) *cobra.Command {
	options := sessionTreeOptions{workspace: "."}
	command := &cobra.Command{
		Use:   "tree",
		Short: "Show all branches and the active leaf",
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return newUsageError(err)
			}
			return validateSessionOptions(options.workspace, options.session)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			request := SessionTreeRequest{
				Workspace: options.workspace,
				Session:   options.session,
			}
			if err := navigator.SessionTree(
				command.Context(),
				request,
				command.OutOrStdout(),
			); err != nil {
				return fmt.Errorf("show session tree: %w", err)
			}
			return nil
		},
	}
	addSessionFlags(command, &options.workspace, &options.session)
	return command
}

func newSessionCheckoutCommand(navigator SessionNavigator) *cobra.Command {
	options := sessionCheckoutOptions{workspace: "."}
	command := &cobra.Command{
		Use:   "checkout",
		Short: "Move the active leaf without deleting later branches",
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return newUsageError(err)
			}
			if err := validateSessionOptions(
				options.workspace,
				options.session,
			); err != nil {
				return err
			}
			if !command.Flags().Changed("entry") {
				return newUsageError(errors.New("entry is required"))
			}
			if strings.TrimSpace(options.entry) == "" {
				return newUsageError(errors.New("entry must not be blank"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			request := SessionCheckoutRequest{
				Workspace: options.workspace,
				Session:   options.session,
				Entry:     options.entry,
			}
			if err := navigator.CheckoutSession(
				command.Context(),
				request,
				command.OutOrStdout(),
			); err != nil {
				return fmt.Errorf("checkout session entry: %w", err)
			}
			return nil
		},
	}
	addSessionFlags(command, &options.workspace, &options.session)
	command.Flags().StringVar(
		&options.entry,
		"entry",
		"",
		"tree entry id to activate, or root",
	)
	return command
}

func validateSessionOptions(workspace string, session string) error {
	if strings.TrimSpace(workspace) == "" {
		return newUsageError(errors.New("workspace must not be blank"))
	}
	if strings.TrimSpace(session) == "" {
		return newUsageError(errors.New("session must not be blank"))
	}
	return nil
}

func addSessionFlags(
	command *cobra.Command,
	workspace *string,
	session *string,
) {
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newUsageError(err)
	})
	command.Flags().StringVar(
		workspace,
		"workspace",
		*workspace,
		"working directory recorded by the Session",
	)
	command.Flags().StringVar(
		session,
		"session",
		"",
		"existing Session JSONL file",
	)
}

type sessionTreeOptions struct {
	workspace string
	session   string
}

type sessionCheckoutOptions struct {
	workspace string
	session   string
	entry     string
}
