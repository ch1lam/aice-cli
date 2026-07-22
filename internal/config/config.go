// Package config loads AICE process configuration from explicit sources.
package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	// EnvDeepSeekAPIKey authenticates requests to DeepSeek.
	EnvDeepSeekAPIKey = "AICE_DEEPSEEK_API_KEY"
	// EnvDeepSeekBaseURL overrides DeepSeek's official API endpoint.
	EnvDeepSeekBaseURL = "AICE_DEEPSEEK_BASE_URL"
)

// Config contains the process settings needed by the current AICE slice.
type Config struct {
	DeepSeekAPIKey  string
	DeepSeekBaseURL string
}

// LookupEnv resolves one environment variable.
type LookupEnv func(key string) (string, bool)

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return LoadEnv(os.LookupEnv)
}

// LoadEnv reads configuration with an injectable environment lookup.
func LoadEnv(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("config: environment lookup is required")
	}

	apiKey, _ := lookup(EnvDeepSeekAPIKey)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Config{}, fmt.Errorf("config: %s is required", EnvDeepSeekAPIKey)
	}

	baseURL, _ := lookup(EnvDeepSeekBaseURL)
	return Config{
		DeepSeekAPIKey:  apiKey,
		DeepSeekBaseURL: strings.TrimSpace(baseURL),
	}, nil
}
