package config

import (
	"context"
	"fmt"
	"maps"
	"strings"
)

// providerKey names one API-key provider's configuration. Files store
// <name>_api_key (normally in auth.json) and <name>_base_url; the environment
// uses the listed variables. Config maps are keyed by provider ID.
type providerKey struct {
	id, name, apiKeyEnv, baseURLEnv string
	// keyless providers accept a blank key, which clears the saved one.
	keyless bool
}

var providerKeys = []providerKey{
	{"deepseek", "deepseek", EnvDeepSeekAPIKey, EnvDeepSeekBaseURL, false},
	{"opencode-go", "opencode", EnvOpenCodeAPIKey, EnvOpenCodeBaseURL, false},
	{"openai", "openai", EnvOpenAIAPIKey, EnvOpenAIBaseURL, false},
	{"anthropic", "anthropic", EnvAnthropicAPIKey, EnvAnthropicBaseURL, false},
	{"kimi-coding", "kimi", EnvKimiAPIKey, EnvKimiBaseURL, false},
	{"zhipu-coding", "zhipu_coding", EnvZhipuCodingAPIKey, EnvZhipuCodingBaseURL, false},
	{"zhipu", "zhipu", EnvZhipuAPIKey, EnvZhipuBaseURL, false},
	{"moonshot", "moonshot", EnvMoonshotAPIKey, EnvMoonshotBaseURL, false},
	{"aihubmix", "aihubmix", EnvAiHubMixAPIKey, EnvAiHubMixBaseURL, false},
	{"custom", "custom", EnvCustomAPIKey, EnvCustomBaseURL, true},
}

func findProviderKey(id string) (providerKey, bool) {
	for _, key := range providerKeys {
		if key.id == id {
			return key, true
		}
	}
	return providerKey{}, false
}

// APIKeyEnvironment returns the environment variable that supplies provider
// id's API key, or "" for providers without one.
func APIKeyEnvironment(id string) string {
	key, _ := findProviderKey(id)
	return key.apiKeyEnv
}

// WithAPIKey returns a snapshot whose provider id uses apiKey. Snapshots
// share their maps, so callers change them only through this method.
func (c Config) WithAPIKey(id, apiKey string) Config {
	c.APIKeys = maps.Clone(c.APIKeys)
	if c.APIKeys == nil {
		c.APIKeys = make(map[string]string)
	}
	c.APIKeys[id] = apiKey
	return c
}

// SaveAPIKey stores provider id's API key in the default global auth file
// and returns that file's path.
func SaveAPIKey(id, apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	return paths.GlobalAuth, SaveAPIKeyFile(paths, id, apiKey)
}

// SaveAPIKeyFile stores provider id's API key in an explicit global auth file.
func SaveAPIKeyFile(paths Paths, id, apiKey string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	key, ok := findProviderKey(id)
	if !ok {
		return fmt.Errorf("config: provider %q has no API key", id)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" && !key.keyless {
		return fmt.Errorf("config: %s API key is required", id)
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return fmt.Errorf("config: %s API key must be one line", id)
	}
	return patchFile(context.Background(), paths.GlobalAuth, map[string]any{key.name + "_api_key": apiKey})
}

// takeProviderValues moves provider keys out of flat configuration values
// into maps keyed by provider ID, validating each value.
func takeProviderValues(values map[string]any) (apiKeys, baseURLs map[string]string, err error) {
	apiKeys, baseURLs = make(map[string]string), make(map[string]string)
	for _, key := range providerKeys {
		for suffix, target := range map[string]map[string]string{"_api_key": apiKeys, "_base_url": baseURLs} {
			field := key.name + suffix
			raw, ok := values[field]
			if !ok {
				continue
			}
			delete(values, field)
			text, ok := raw.(string)
			if !ok {
				return nil, nil, fmt.Errorf("config: invalid type for %s (expected string)", field)
			}
			if text = strings.TrimSpace(text); text != "" {
				target[key.id] = text
			}
		}
		if strings.ContainsAny(apiKeys[key.id], "\r\n") {
			return nil, nil, fmt.Errorf("config: %s_api_key must be one line", key.name)
		}
		if err := validateCustomBaseURL(baseURLs[key.id]); err != nil {
			return nil, nil, fmt.Errorf("config: %s_base_url: %w", key.name, err)
		}
	}
	return apiKeys, baseURLs, nil
}

// putProviderValues is the inverse of takeProviderValues for non-empty values.
func putProviderValues(values map[string]any, apiKeys, baseURLs map[string]string) {
	for _, key := range providerKeys {
		if apiKey := apiKeys[key.id]; apiKey != "" {
			values[key.name+"_api_key"] = apiKey
		}
		if baseURL := baseURLs[key.id]; baseURL != "" {
			values[key.name+"_base_url"] = baseURL
		}
	}
}

func providerEnvironmentVariables(variables map[string]string) {
	for _, key := range providerKeys {
		variables[key.name+"_api_key"] = key.apiKeyEnv
		variables[key.name+"_base_url"] = key.baseURLEnv
	}
}
