package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// AuthRequest selects an explicit subscription credential operation.
type AuthRequest struct {
	Action     string
	Provider   string
	DeviceCode bool
}

// Authenticator owns OAuth login and global credential lifecycle.
type Authenticator interface {
	Auth(context.Context, AuthRequest, io.Writer) error
}

func newAuthCommand(authenticator Authenticator) *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage subscription login"}
	for _, action := range []string{"login", "status", "logout"} {
		request := AuthRequest{Action: action, Provider: "openai-codex"}
		child := &cobra.Command{
			Use: action, Short: action + " for a subscription provider",
			Args: func(command *cobra.Command, args []string) error { return newUsageError(cobra.NoArgs(command, args)) },
			RunE: func(command *cobra.Command, _ []string) error {
				if request.Provider != "openai-codex" {
					return newUsageError(fmt.Errorf("subscription auth supports openai-codex; use config set-key for API keys"))
				}
				return authenticator.Auth(command.Context(), request, command.OutOrStdout())
			},
		}
		child.Flags().StringVar(&request.Provider, "provider", request.Provider, "subscription provider (openai-codex)")
		if action == "login" {
			child.Flags().BoolVar(&request.DeviceCode, "device-code", false, "use device code login for remote or headless terminals")
		}
		command.AddCommand(child)
	}
	return command
}
