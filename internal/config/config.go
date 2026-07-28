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

	settingsFileName = "settings.json"
	authFileName     = "auth.json"
)

const (
	settingsKeyProvider = "provider"
	settingsKeyModel    = "model"
	settingsKeyThinking = "thinking"
)

// Scope identifies one persistent settings layer.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Setting identifies one setting that can be persisted by a command.
type Setting string

const (
	SettingProvider Setting = settingsKeyProvider
	SettingModel    Setting = settingsKeyModel
	SettingThinking Setting = settingsKeyThinking
)

// Settings contains non-secret model defaults. Empty fields inherit the next
// lower-precedence layer.
type Settings struct {
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	Thinking llm.ThinkingLevel `json:"thinking,omitempty"`
}

// Paths identifies all files used to resolve configuration.
type Paths struct {
	GlobalSettings  string
	ProjectSettings string
	GlobalAuth      string
}

// Config contains the effective process settings needed by AICE.
type Config struct {
	Provider        string
	Model           string
	Thinking        llm.ThinkingLevel
	DeepSeekAPIKey  string
	DeepSeekBaseURL string
	Paths           Paths
}

type authFile struct {
	DeepSeekAPIKey string `json:"deepseek_api_key,omitempty"`
}

// LookupEnv resolves one environment variable.
type LookupEnv func(key string) (string, bool)

// Load resolves configuration for one workspace.
func Load(workspace string) (Config, error) {
	paths, err := DefaultPaths(workspace)
	if err != nil {
		return Config{}, err
	}
	return LoadFiles(paths, os.LookupEnv)
}

// DefaultPaths returns the Pi-inspired global and project configuration paths.
func DefaultPaths(workspace string) (Paths, error) {
	if strings.TrimSpace(workspace) == "" {
		return Paths{}, fmt.Errorf("config: workspace is required")
	}
	workspacePath, err := filepath.Abs(workspace)
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve workspace: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve home directory: %w", err)
	}
	globalDir := filepath.Join(home, ".aice")
	return Paths{
		GlobalSettings:  filepath.Join(globalDir, settingsFileName),
		ProjectSettings: filepath.Join(workspacePath, ".aice", settingsFileName),
		GlobalAuth:      filepath.Join(globalDir, authFileName),
	}, nil
}

// LoadFiles resolves explicit files with the precedence:
// environment > project settings > global settings.
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
	return Config{
		Provider:        settings.Provider,
		Model:           settings.Model,
		Thinking:        settings.Thinking,
		DeepSeekAPIKey:  apiKey,
		DeepSeekBaseURL: strings.TrimSpace(baseURL),
		Paths:           paths,
	}, nil
}

// SaveSetting updates one settings file without copying inherited values into
// that scope.
func SaveSetting(
	workspace string,
	scope Scope,
	setting Setting,
	value string,
) error {
	paths, err := DefaultPaths(workspace)
	if err != nil {
		return err
	}
	return SaveSettingFile(paths, scope, setting, value)
}

// SaveDeepSeekAPIKey stores the DeepSeek credential in the global auth file.
func SaveDeepSeekAPIKey(workspace string, apiKey string) (string, error) {
	paths, err := DefaultPaths(workspace)
	if err != nil {
		return "", err
	}
	if err := SaveDeepSeekAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveDeepSeekAPIKeyFile stores one credential in an explicit global auth
// file. Project settings never receive credentials.
func SaveDeepSeekAPIKeyFile(paths Paths, apiKey string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("config: DeepSeek API key is required")
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return fmt.Errorf("config: DeepSeek API key must be one line")
	}
	if err := writeJSON(paths.GlobalAuth, authFile{
		DeepSeekAPIKey: apiKey,
	}); err != nil {
		return fmt.Errorf("config: write global auth: %w", err)
	}
	return nil
}

// SaveSettingFile updates one explicit settings layer.
func SaveSettingFile(
	paths Paths,
	scope Scope,
	setting Setting,
	value string,
) error {
	if err := paths.validate(); err != nil {
		return err
	}
	path, err := paths.settingsPath(scope)
	if err != nil {
		return err
	}
	settings, _, err := readSettings(path)
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
	if err := writeJSON(path, settings); err != nil {
		return fmt.Errorf("config: write %s settings: %w", scope, err)
	}
	return nil
}

func loadSettings(paths Paths, lookup LookupEnv) (Settings, error) {
	registry := viper.New()
	registry.SetConfigType("json")

	loaded := false
	for _, path := range []string{
		paths.GlobalSettings,
		paths.ProjectSettings,
	} {
		_, data, err := readSettings(path)
		if err != nil {
			return Settings{}, err
		}
		if data == nil {
			continue
		}
		if !loaded {
			err = registry.ReadConfig(bytes.NewReader(data))
			loaded = true
		} else {
			err = registry.MergeConfig(bytes.NewReader(data))
		}
		if err != nil {
			return Settings{}, fmt.Errorf(
				"config: merge settings %s: %w",
				path,
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
		"global settings":  p.GlobalSettings,
		"project settings": p.ProjectSettings,
		"global auth":      p.GlobalAuth,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("config: %s path is required", name)
		}
	}
	return nil
}

func (p Paths) settingsPath(scope Scope) (string, error) {
	switch scope {
	case ScopeGlobal:
		return p.GlobalSettings, nil
	case ScopeProject:
		return p.ProjectSettings, nil
	default:
		return "", fmt.Errorf("config: unsupported settings scope %q", scope)
	}
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
