// Package provider defines the provider-neutral surface consumed by the app
// composition root. Concrete providers implement Provider without holding
// credentials; request-time credentials are supplied through New.
package provider

import (
	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Provider is the registry surface the composition root injects. A Provider
// value must be constructible without credentials so the app can assemble the
// registry at startup; New builds the credentialed model service at run time.
type Provider interface {
	// ProviderID identifies the provider in AICE configuration.
	ProviderID() llm.ProviderID
	// Label is the provider's display name.
	Label() string
	// MenuDescription is the provider's interactive menu description.
	MenuDescription() string
	// Models returns the provider's model catalog.
	Models() []llm.Model
	// DefaultModel returns the model used when none is selected.
	DefaultModel() llm.Model
	// Configured reports whether the configuration carries a credential.
	Configured(configuration config.Config) bool
	// New constructs the credentialed model service for a configuration.
	New(configuration config.Config) (agent.Model, error)
	// SaveAPIKey persists a credential in the global auth file.
	SaveAPIKey(apiKey string) (string, error)
	// ApplyAPIKey stores a credential in the configuration.
	ApplyAPIKey(configuration *config.Config, apiKey string)
	// CredentialNotConfiguredError describes the missing credential.
	CredentialNotConfiguredError() error
}
