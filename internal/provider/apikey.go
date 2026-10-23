package provider

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// APIKey implements the credential methods of a provider authenticated by
// one API key in AICE configuration.
type APIKey struct {
	Provider llm.ProviderID
	// Label names the credential's owner in errors, for example "OpenAI".
	Label string
}

// Value returns the configured API key.
func (k APIKey) Value(configuration config.Config) string {
	return configuration.APIKeys[string(k.Provider)]
}

// BaseURL returns the configured endpoint override.
func (k APIKey) BaseURL(configuration config.Config) string {
	return configuration.BaseURLs[string(k.Provider)]
}

// Configured reports whether the configuration carries a non-blank key.
func (k APIKey) Configured(configuration config.Config) bool {
	return strings.TrimSpace(k.Value(configuration)) != ""
}

// Save persists the key in the global auth file and returns its path.
func (k APIKey) Save(apiKey string) (string, error) {
	return config.SaveAPIKey(string(k.Provider), apiKey)
}

// Apply stores the key in the configuration snapshot.
func (k APIKey) Apply(configuration *config.Config, apiKey string) {
	*configuration = configuration.WithAPIKey(string(k.Provider), apiKey)
}

// NotConfiguredError describes the missing key and how to supply it.
func (k APIKey) NotConfiguredError() error {
	return fmt.Errorf(
		"%s API key is not configured; run /login in interactive mode or set %s",
		k.Label, config.APIKeyEnvironment(string(k.Provider)),
	)
}
