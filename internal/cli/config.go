package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const maximumCredentialBytes = 64 * 1024

// APIKeyRequest contains one credential entered through the CLI. Provider
// selects which provider's credential file entry receives the key.
type APIKeyRequest struct {
	Provider string
	APIKey   string
}

// Configurator persists global credentials without exposing storage details to
// the command layer.
type Configurator interface {
	SaveAPIKey(ctx context.Context, request APIKeyRequest) (string, error)
}

func newConfigCommand(configurator Configurator) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Configure AICE credentials",
		Args: func(command *cobra.Command, args []string) error {
			return newUsageError(cobra.NoArgs(command, args))
		},
	}
	command.AddCommand(newSetAPIKeyCommand(configurator))
	return command
}

func newSetAPIKeyCommand(configurator Configurator) *cobra.Command {
	provider := "deepseek"
	command := &cobra.Command{
		Use:   "set-key",
		Short: "Read an API key from stdin and store it globally",
		Args: func(command *cobra.Command, args []string) error {
			return newUsageError(cobra.NoArgs(command, args))
		},
		RunE: func(command *cobra.Command, _ []string) error {
			apiKey, err := readAPIKey(command)
			if err != nil {
				return err
			}
			path, err := configurator.SaveAPIKey(
				command.Context(),
				APIKeyRequest{
					Provider: provider,
					APIKey:   apiKey,
				},
			)
			if err != nil {
				return fmt.Errorf("save API key: %w", err)
			}
			if _, err := fmt.Fprintf(
				command.OutOrStdout(),
				"Saved %s API key to %s.\n",
				provider,
				path,
			); err != nil {
				return fmt.Errorf("write configuration result: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(
		&provider,
		"provider",
		"deepseek",
		"provider whose credential to store (deepseek or opencode-go)",
	)
	return command
}

func readAPIKey(command *cobra.Command) (string, error) {
	input := command.InOrStdin()
	data, err := io.ReadAll(io.LimitReader(input, maximumCredentialBytes+1))
	if err != nil {
		return "", fmt.Errorf("read API key: %w", err)
	}
	if len(data) > maximumCredentialBytes {
		return "", errors.New("API key input is too large")
	}
	return validateAPIKey(string(data))
}

func validateAPIKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", newUsageError(errors.New("API key must not be blank"))
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", newUsageError(errors.New("API key must be one line"))
	}
	return value, nil
}
