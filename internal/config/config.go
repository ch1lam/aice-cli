// Package config loads and persists AICE process configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	// EnvProvider selects the provider used for model requests.
	EnvProvider = "AICE_PROVIDER"
	// EnvModel selects the provider model used for model requests.
	EnvModel = "AICE_MODEL"
	// EnvThinking selects the requested reasoning level. "off" disables it.
	EnvThinking = "AICE_THINKING"
	// EnvDeepSeekAPIKey authenticates requests to DeepSeek.
	EnvDeepSeekAPIKey = "AICE_DEEPSEEK_API_KEY"
	// EnvDeepSeekBaseURL overrides DeepSeek's official API endpoint.
	EnvDeepSeekBaseURL = "AICE_DEEPSEEK_BASE_URL"
	// EnvOpenCodeAPIKey authenticates requests to OpenCode Go.
	EnvOpenCodeAPIKey = "AICE_OPENCODE_API_KEY"
	// EnvOpenCodeBaseURL overrides OpenCode Go's official API endpoint.
	EnvOpenCodeBaseURL = "AICE_OPENCODE_BASE_URL"

	settingsFileName = "settings.json"
	authFileName     = "auth.json"
)

const (
	settingsKeyProvider = "provider"
	settingsKeyModel    = "model"
	settingsKeyThinking = "thinking"
)

// Setting identifies one setting that can be persisted by a command.
type Setting string

const (
	SettingProvider Setting = settingsKeyProvider
	SettingModel    Setting = settingsKeyModel
	SettingThinking Setting = settingsKeyThinking
)

// Settings contains non-secret global model defaults.
type Settings struct {
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	Thinking llm.ThinkingLevel `json:"thinking,omitempty"`
}

// Paths identifies all files used to resolve configuration and the directory
// where AICE installs helper executables.
type Paths struct {
	GlobalSettings string
	GlobalAuth     string
	BinDir         string
}

// Config contains the effective process settings needed by AICE.
type Config struct {
	Provider        string
	Model           string
	Thinking        llm.ThinkingLevel
	DeepSeekAPIKey  string
	DeepSeekBaseURL string
	OpenCodeAPIKey  string
	OpenCodeBaseURL string
	Paths           Paths
}

type authFile struct {
	DeepSeekAPIKey string `json:"deepseek_api_key,omitempty"`
	OpenCodeAPIKey string `json:"opencode_api_key,omitempty"`
}

// LookupEnv resolves one environment variable.
type LookupEnv func(key string) (string, bool)

// Load resolves global configuration.
func Load() (Config, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return Config{}, err
	}
	return LoadFiles(paths, os.LookupEnv)
}

// DefaultPaths returns AICE's global configuration paths.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve home directory: %w", err)
	}
	globalDir := filepath.Join(home, ".aice")
	return Paths{
		GlobalSettings: filepath.Join(globalDir, settingsFileName),
		GlobalAuth:     filepath.Join(globalDir, authFileName),
		BinDir:         filepath.Join(globalDir, "bin"),
	}, nil
}

// LoadFiles resolves explicit files with the precedence:
// environment > global settings.
// Credentials are global-only and may be overridden by the environment.
func LoadFiles(paths Paths, lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("config: environment lookup is required")
	}
	if err := paths.validate(); err != nil {
		return Config{}, err
	}

	settings, err := loadSettings(paths, lookup)
	if err != nil {
		return Config{}, err
	}
	auth, err := loadAuth(paths.GlobalAuth)
	if err != nil {
		return Config{}, err
	}

	apiKey := strings.TrimSpace(auth.DeepSeekAPIKey)
	if value, exists := lookup(EnvDeepSeekAPIKey); exists {
		if value = strings.TrimSpace(value); value != "" {
			apiKey = value
		}
	}
	baseURL, _ := lookup(EnvDeepSeekBaseURL)

	openCodeAPIKey := strings.TrimSpace(auth.OpenCodeAPIKey)
	if value, exists := lookup(EnvOpenCodeAPIKey); exists {
		if value = strings.TrimSpace(value); value != "" {
			openCodeAPIKey = value
		}
	}
	openCodeBaseURL, _ := lookup(EnvOpenCodeBaseURL)

	return Config{
		Provider:        settings.Provider,
		Model:           settings.Model,
		Thinking:        settings.Thinking,
		DeepSeekAPIKey:  apiKey,
		DeepSeekBaseURL: strings.TrimSpace(baseURL),
		OpenCodeAPIKey:  openCodeAPIKey,
		OpenCodeBaseURL: strings.TrimSpace(openCodeBaseURL),
		Paths:           paths,
	}, nil
}

// SaveSetting updates the global settings file.
func SaveSetting(setting Setting, value string) error {
	paths, err := DefaultPaths()
	if err != nil {
		return err
	}
	return SaveSettingFile(paths, setting, value)
}

// SaveDeepSeekAPIKey stores the DeepSeek credential in the global auth file.
func SaveDeepSeekAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveDeepSeekAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveDeepSeekAPIKeyFile stores the DeepSeek credential in an explicit global
// auth file, preserving any other provider credentials already present.
// Credentials are never stored in settings.
func SaveDeepSeekAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: DeepSeek API key is required")
	}
	return saveAPIKeyFile(paths, "DeepSeek", apiKey, func(auth *authFile) {
		auth.DeepSeekAPIKey = apiKey
	})
}

// SaveOpenCodeAPIKey stores the OpenCode Go credential in the global auth file.
func SaveOpenCodeAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveOpenCodeAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveOpenCodeAPIKeyFile stores the OpenCode Go credential in an explicit
// global auth file, preserving any other provider credentials already present.
func SaveOpenCodeAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: OpenCode Go API key is required")
	}
	return saveAPIKeyFile(paths, "OpenCode Go", apiKey, func(auth *authFile) {
		auth.OpenCodeAPIKey = apiKey
	})
}

// saveAPIKeyFile reads the existing auth file, applies one credential update,
// and writes the merged result back so different provider credentials are not
// clobbered. The one-line rule validates the key being written, not credentials
// belonging to other providers. Credentials are never stored in settings.
func saveAPIKeyFile(
	paths Paths,
	providerName, apiKey string,
	update func(*authFile),
) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return fmt.Errorf("config: %s API key must be one line", providerName)
	}
	auth, err := loadAuth(paths.GlobalAuth)
	if err != nil {
		return err
	}
	update(&auth)
	if err := writeJSON(paths.GlobalAuth, auth); err != nil {
		return fmt.Errorf("config: write global auth: %w", err)
	}
	return nil
}

// SaveSettingFile updates one explicit global settings file.
func SaveSettingFile(
	paths Paths,
	setting Setting,
	value string,
) error {
	if err := paths.validate(); err != nil {
		return err
	}
	settings, _, err := readSettings(paths.GlobalSettings)
	if err != nil {
		return err
	}

	value = strings.TrimSpace(value)
	switch setting {
	case SettingProvider:
		settings.Provider = value
	case SettingModel:
		settings.Model = value
	case SettingThinking:
		settings.Thinking = llm.ThinkingLevel(value)
	default:
		return fmt.Errorf("config: unsupported setting %q", setting)
	}
	if err := settings.validate(); err != nil {
		return err
	}
	if err := writeJSON(paths.GlobalSettings, settings); err != nil {
		return fmt.Errorf("config: write global settings: %w", err)
	}
	return nil
}

func loadSettings(paths Paths, lookup LookupEnv) (Settings, error) {
	registry := viper.New()
	registry.SetConfigType("json")

	_, data, err := readSettings(paths.GlobalSettings)
	if err != nil {
		return Settings{}, err
	}
	if data != nil {
		if err := registry.ReadConfig(bytes.NewReader(data)); err != nil {
			return Settings{}, fmt.Errorf(
				"config: read settings %s: %w",
				paths.GlobalSettings,
				err,
			)
		}
	}

	for key, environment := range map[string]string{
		settingsKeyProvider: EnvProvider,
		settingsKeyModel:    EnvModel,
		settingsKeyThinking: EnvThinking,
	} {
		if value, exists := lookup(environment); exists {
			if value = strings.TrimSpace(value); value != "" {
				registry.Set(key, value)
			}
		}
	}

	settings := Settings{
		Provider: strings.TrimSpace(registry.GetString(settingsKeyProvider)),
		Model:    strings.TrimSpace(registry.GetString(settingsKeyModel)),
		Thinking: llm.ThinkingLevel(strings.TrimSpace(
			registry.GetString(settingsKeyThinking),
		)),
	}
	if err := settings.validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func loadAuth(path string) (authFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return authFile{}, nil
	}
	if err != nil {
		return authFile{}, fmt.Errorf("config: read auth %s: %w", path, err)
	}
	var auth authFile
	if err := decodeJSON(path, data, &auth); err != nil {
		return authFile{}, err
	}
	auth.DeepSeekAPIKey = strings.TrimSpace(auth.DeepSeekAPIKey)
	auth.OpenCodeAPIKey = strings.TrimSpace(auth.OpenCodeAPIKey)
	return auth, nil
}

func readSettings(path string) (Settings, []byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil, nil
	}
	if err != nil {
		return Settings{}, nil, fmt.Errorf(
			"config: read settings %s: %w",
			path,
			err,
		)
	}
	var settings Settings
	if err := decodeJSON(path, data, &settings); err != nil {
		return Settings{}, nil, err
	}
	if err := settings.validate(); err != nil {
		return Settings{}, nil, fmt.Errorf(
			"config: validate settings %s: %w",
			path,
			err,
		)
	}
	return settings, data, nil
}

func decodeJSON(path string, data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("config: decode %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("config: decode %s: multiple JSON values", path)
		}
		return fmt.Errorf("config: decode trailing %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) (returnErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	data = append(data, '\n')

	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("set file permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}

func (p Paths) validate() error {
	for name, path := range map[string]string{
		"global settings": p.GlobalSettings,
		"global auth":     p.GlobalAuth,
		"bin dir":         p.BinDir,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("config: %s path is required", name)
		}
	}
	return nil
}

func (s Settings) validate() error {
	if strings.ContainsAny(s.Provider, " \t\r\n") {
		return fmt.Errorf("provider must not contain whitespace")
	}
	if strings.ContainsAny(s.Model, " \t\r\n") {
		return fmt.Errorf("model must not contain whitespace")
	}
	switch s.Thinking {
	case llm.ThinkingLevelUnknown,
		llm.ThinkingLevelOff,
		llm.ThinkingLevelMinimal,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax:
		return nil
	default:
		return fmt.Errorf("unsupported thinking level %q", s.Thinking)
	}
}
