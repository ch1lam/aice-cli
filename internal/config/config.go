// Package config loads and persists AICE process configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/trust"
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
	// EnvOpenAIAPIKey authenticates requests to OpenAI.
	EnvOpenAIAPIKey = "OPENAI_API_KEY"
	// EnvOpenAIBaseURL overrides OpenAI's official API endpoint.
	EnvOpenAIBaseURL = "AICE_OPENAI_BASE_URL"
	// EnvAnthropicAPIKey authenticates requests to Anthropic.
	EnvAnthropicAPIKey = "ANTHROPIC_API_KEY"
	// EnvAnthropicBaseURL overrides Anthropic's official API endpoint.
	EnvAnthropicBaseURL = "AICE_ANTHROPIC_BASE_URL"
	// EnvKimiAPIKey authenticates Kimi Coding Plan requests.
	EnvKimiAPIKey = "KIMI_API_KEY"
	// EnvKimiBaseURL overrides the Kimi Coding Plan endpoint.
	EnvKimiBaseURL = "AICE_KIMI_BASE_URL"
	// EnvZhipuCodingAPIKey authenticates Zhipu Coding Plan requests.
	EnvZhipuCodingAPIKey = "ZHIPU_CODING_API_KEY"
	// EnvZhipuCodingBaseURL overrides the Zhipu Coding Plan endpoint.
	EnvZhipuCodingBaseURL = "AICE_ZHIPU_CODING_BASE_URL"
	// EnvZhipuAPIKey authenticates Zhipu API Platform requests.
	EnvZhipuAPIKey = "ZHIPU_API_KEY"
	// EnvZhipuBaseURL overrides the Zhipu API Platform endpoint.
	EnvZhipuBaseURL = "AICE_ZHIPU_BASE_URL"
	// EnvMoonshotAPIKey authenticates Moonshot API Platform requests.
	EnvMoonshotAPIKey = "MOONSHOT_API_KEY"
	// EnvMoonshotBaseURL overrides the Moonshot API Platform endpoint.
	EnvMoonshotBaseURL = "AICE_MOONSHOT_BASE_URL"
	// EnvAiHubMixAPIKey authenticates requests to AiHubMix.
	EnvAiHubMixAPIKey = "AIHUBMIX_API_KEY"
	// EnvAiHubMixBaseURL overrides the AiHubMix API root.
	EnvAiHubMixBaseURL = "AICE_AIHUBMIX_BASE_URL"
	// EnvCustomAPIKey authenticates requests to a custom OpenAI-compatible endpoint.
	EnvCustomAPIKey = "AICE_CUSTOM_API_KEY"
	// EnvCustomBaseURL overrides the custom OpenAI-compatible endpoint.
	EnvCustomBaseURL = "AICE_CUSTOM_BASE_URL"

	settingsFileName = "settings.json"
	authFileName     = "auth.json"
	trustFileName    = "trust.json"
)

const (
	settingsKeyProvider            = "provider"
	settingsKeyModel               = "model"
	settingsKeyThinking            = "thinking"
	settingsKeyDefaultProjectTrust = "default_project_trust"
	settingsKeyCustomBaseURL       = "custom_base_url"
)

// Setting identifies one setting that can be persisted by a command.
type Setting string

const (
	SettingBrowserHeaded Setting = "browser_headed"
	SettingProvider      Setting = settingsKeyProvider
	SettingModel         Setting = settingsKeyModel
	SettingThinking      Setting = settingsKeyThinking
	SettingCustomBaseURL Setting = settingsKeyCustomBaseURL
)

// ContextWindow identifies a model without treating its identifier as a Viper key.
type ContextWindow struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Tokens   int64  `json:"tokens"`
}

// Settings is the file schema shared by user and project configuration.
// API keys normally live in auth.json, with the same keys and precedence.
type Settings struct {
	BrowserHeaded       bool              `json:"browser_headed,omitempty"`
	MaxTurns            int               `json:"max_turns,omitempty"`
	RunNoProgressLimit  int               `json:"run_no_progress_limit"`
	RunTokenBudget      int64             `json:"run_token_budget,omitempty"`
	RunTimeout          string            `json:"run_timeout,omitempty"`
	ContextWindows      []ContextWindow   `json:"context_windows,omitempty"`
	Provider            string            `json:"provider,omitempty"`
	Model               string            `json:"model,omitempty"`
	Thinking            llm.ThinkingLevel `json:"thinking,omitempty"`
	DefaultProjectTrust trust.Default     `json:"default_project_trust,omitempty"`
	DeepSeekAPIKey      string            `json:"deepseek_api_key,omitempty"`
	DeepSeekBaseURL     string            `json:"deepseek_base_url,omitempty"`
	OpenCodeAPIKey      string            `json:"opencode_api_key,omitempty"`
	OpenCodeBaseURL     string            `json:"opencode_base_url,omitempty"`
	OpenAIAPIKey        string            `json:"openai_api_key,omitempty"`
	OpenAIBaseURL       string            `json:"openai_base_url,omitempty"`
	AnthropicAPIKey     string            `json:"anthropic_api_key,omitempty"`
	AnthropicBaseURL    string            `json:"anthropic_base_url,omitempty"`
	KimiAPIKey          string            `json:"kimi_api_key,omitempty"`
	KimiBaseURL         string            `json:"kimi_base_url,omitempty"`
	ZhipuCodingAPIKey   string            `json:"zhipu_coding_api_key,omitempty"`
	ZhipuCodingBaseURL  string            `json:"zhipu_coding_base_url,omitempty"`
	ZhipuAPIKey         string            `json:"zhipu_api_key,omitempty"`
	ZhipuBaseURL        string            `json:"zhipu_base_url,omitempty"`
	MoonshotAPIKey      string            `json:"moonshot_api_key,omitempty"`
	MoonshotBaseURL     string            `json:"moonshot_base_url,omitempty"`
	AiHubMixAPIKey      string            `json:"aihubmix_api_key,omitempty"`
	AiHubMixBaseURL     string            `json:"aihubmix_base_url,omitempty"`
	CustomAPIKey        string            `json:"custom_api_key,omitempty"`
	CustomBaseURL       string            `json:"custom_base_url,omitempty"`
	NoDepInstall        bool              `json:"no_dep_install,omitempty"`
	NoUpdateCheck       bool              `json:"no_update_check,omitempty"`
}

// Paths identifies configuration sources and helper storage.
type Paths struct {
	GlobalSettings  string
	ProjectSettings string
	GlobalAuth      string
	GlobalTrust     string
	BinDir          string
}

// Config is an immutable effective snapshot owned by one application instance.
// Interactive changes create another snapshot; they never reload file layers.
type Config struct {
	BrowserHeaded       bool
	MaxTurns            int
	RunNoProgressLimit  int
	RunTokenBudget      int64
	RunTimeout          time.Duration
	ContextWindows      map[string]int64
	Provider            string
	Model               string
	Thinking            llm.ThinkingLevel
	DefaultProjectTrust trust.Default
	DeepSeekAPIKey      string
	DeepSeekBaseURL     string
	OpenCodeAPIKey      string
	OpenCodeBaseURL     string
	OpenAIAPIKey        string
	OpenAIBaseURL       string
	AnthropicAPIKey     string
	AnthropicBaseURL    string
	KimiAPIKey          string
	KimiBaseURL         string
	ZhipuCodingAPIKey   string
	ZhipuCodingBaseURL  string
	ZhipuAPIKey         string
	ZhipuBaseURL        string
	MoonshotAPIKey      string
	MoonshotBaseURL     string
	AiHubMixAPIKey      string
	AiHubMixBaseURL     string
	CustomAPIKey        string
	CustomBaseURL       string
	NoDepInstall        bool
	NoUpdateCheck       bool
	CodexCredentials    CodexCredentials
	Web                 WebConfig
	Paths               Paths
	Diagnostics         []string
	startupOverrides    map[string]bool
	// webCredentials and webEnv let WithWeb re-resolve credential references
	// after an interactive save without rereading files or the environment.
	webCredentials map[string]string
	webEnv         func(string) (string, bool)

	ClaudeSubscriptionCredentials ClaudeSubscriptionCredentials
}

// LoadOptions supplies invocation-specific configuration inputs. Files and
// flags are read once. Environment is opt-in for isolated callers and tests.
type LoadOptions struct {
	Workspace   string
	Environment bool
	BindFlags   func(*viper.Viper) error
	// TrustProject decides whether to load project-controlled values using
	// only the already composed user/env/flag policy. Nil allows explicit
	// LoadFiles callers to supply trusted files; production must provide it.
	TrustProject func(Paths, trust.Default) (bool, error)
}

// Load resolves the process environment and optional workspace configuration.
func Load(options LoadOptions) (Config, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return Config{}, err
	}
	if options.Workspace != "" {
		if options.TrustProject == nil {
			return Config{}, errors.New("config: project trust resolver is required")
		}
		root, err := filepath.Abs(options.Workspace)
		if err != nil {
			return Config{}, fmt.Errorf("config: resolve workspace: %w", err)
		}
		paths.ProjectSettings = filepath.Join(root, ".aice", settingsFileName)
	}
	options.Environment = true
	return LoadFiles(paths, options)
}

// DefaultPaths returns AICE's user configuration paths.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".aice")
	return Paths{GlobalSettings: filepath.Join(root, settingsFileName), GlobalAuth: filepath.Join(root, authFileName), GlobalTrust: filepath.Join(root, trustFileName), BinDir: filepath.Join(root, "bin")}, nil
}

// EnvironmentVariables lists the explicitly supported inputs. Registering
// every key also makes env-only values visible to Viper's AllSettings.
func EnvironmentVariables() map[string]string {
	return map[string]string{
		"browser_headed":        "AICE_BROWSER_HEADED",
		"max_turns":             "AICE_MAX_TURNS",
		"run_no_progress_limit": "AICE_RUN_NO_PROGRESS_LIMIT",
		"run_token_budget":      "AICE_RUN_TOKEN_BUDGET", "run_timeout": "AICE_RUN_TIMEOUT",
		"provider": EnvProvider, "model": EnvModel, "thinking": EnvThinking,
		"default_project_trust": "AICE_DEFAULT_PROJECT_TRUST",
		"context_windows":       "AICE_CONTEXT_WINDOWS",
		"no_dep_install":        "AICE_NO_DEP_INSTALL", "no_update_check": "AICE_NO_UPDATE_CHECK",
		"deepseek_api_key":      EnvDeepSeekAPIKey,
		"deepseek_base_url":     EnvDeepSeekBaseURL,
		"opencode_api_key":      EnvOpenCodeAPIKey,
		"opencode_base_url":     EnvOpenCodeBaseURL,
		"openai_api_key":        EnvOpenAIAPIKey,
		"openai_base_url":       EnvOpenAIBaseURL,
		"anthropic_api_key":     EnvAnthropicAPIKey,
		"anthropic_base_url":    EnvAnthropicBaseURL,
		"kimi_api_key":          EnvKimiAPIKey,
		"kimi_base_url":         EnvKimiBaseURL,
		"zhipu_coding_api_key":  EnvZhipuCodingAPIKey,
		"zhipu_coding_base_url": EnvZhipuCodingBaseURL,
		"zhipu_api_key":         EnvZhipuAPIKey,
		"zhipu_base_url":        EnvZhipuBaseURL,
		"moonshot_api_key":      EnvMoonshotAPIKey,
		"moonshot_base_url":     EnvMoonshotBaseURL,
		"aihubmix_api_key":      EnvAiHubMixAPIKey,
		"aihubmix_base_url":     EnvAiHubMixBaseURL,
		"custom_api_key":        EnvCustomAPIKey,
		"custom_base_url":       EnvCustomBaseURL,
	}
}

func newRegistry(options LoadOptions) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigType("json")
	if options.Environment {
		for key, env := range EnvironmentVariables() {
			if err := v.BindEnv(key, env); err != nil {
				return nil, err
			}
		}
	}
	if options.BindFlags != nil {
		if err := options.BindFlags(v); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// LoadFiles composes flags > env > project > user > defaults, then validates
// only the result. An unparseable source contributes no values.
func LoadFiles(paths Paths, options LoadOptions) (Config, error) {
	if err := paths.validate(); err != nil {
		return Config{}, err
	}
	v, err := newRegistry(options)
	if err != nil {
		return Config{}, err
	}
	high, err := newRegistry(options)
	if err != nil {
		return Config{}, err
	}
	v.SetDefault("default_project_trust", string(trust.DefaultAsk))
	v.SetDefault("thinking", string(llm.DefaultThinkingLevel))
	v.SetDefault("no_dep_install", false)
	v.SetDefault("no_update_check", false)
	v.SetDefault("browser_headed", false)
	v.SetDefault("run_no_progress_limit", 8)
	var diagnostics []string
	var webValues webLayer
	fileValues := make(map[string]any)
	for _, path := range []string{paths.GlobalSettings, paths.GlobalAuth, paths.ProjectSettings} {
		if path == "" {
			continue
		}
		if path == paths.ProjectSettings && options.TrustProject != nil {
			// Invalid bootstrap policy asks rather than granting trust. The
			// complete effective configuration is still validated after merging.
			policy := trust.DefaultAsk
			if value, ok := v.Get("default_project_trust").(string); ok {
				switch trust.Default(strings.TrimSpace(value)) {
				case trust.DefaultAlways:
					policy = trust.DefaultAlways
				case trust.DefaultNever:
					policy = trust.DefaultNever
				}
			}
			allowed, err := options.TrustProject(paths, policy)
			if err != nil {
				return Config{}, err
			}
			if !allowed {
				continue
			}
		}
		values, err := readValues(path)
		if err != nil {
			var syntax *sourceSyntaxError
			if !errors.As(err, &syntax) {
				return Config{}, err
			}
			diagnostics = append(diagnostics, fmt.Sprintf("Ignored unparseable configuration %s", path))
			continue
		}
		// Nested web objects are replaced per layer outside Viper's deep merge.
		if err := webValues.extractWebValues(path, values, path == paths.ProjectSettings); err != nil {
			return Config{}, err
		}
		// Every schema field is a scalar or a complete array. Replace fields
		// before handing the file layer to Viper: MergeConfigMap otherwise
		// retains an invalid lower-layer object when a scalar replaces it.
		for key, value := range values {
			fileValues[strings.ToLower(key)] = value
		}
		if err := v.ReadConfig(strings.NewReader("{}")); err != nil {
			return Config{}, err
		}
		if err := v.MergeConfigMap(fileValues); err != nil {
			return Config{}, err
		}
		if path == paths.ProjectSettings {
			if err := high.MergeConfigMap(values); err != nil {
				return Config{}, err
			}
		}
	}
	c, err := decodeEffective(v)
	if err != nil {
		return Config{}, err
	}
	c.Paths, c.Diagnostics = paths, diagnostics
	if options.Environment {
		c.webEnv = os.LookupEnv
	}
	c.webCredentials = webValues.credentials
	effectiveWebConfig, webDiagnostics, err := effectiveWeb(webValues, paths.ProjectSettings, c.webEnv)
	if err != nil {
		return Config{}, err
	}
	c.Web = effectiveWebConfig
	c.Diagnostics = append(c.Diagnostics, webDiagnostics...)
	c.startupOverrides = make(map[string]bool)
	for key := range high.AllSettings() {
		c.startupOverrides[key] = true
	}
	c.CodexCredentials, err = LoadCodexCredentials(paths)
	if err != nil {
		var syntax *sourceSyntaxError
		if !errors.As(err, &syntax) {
			return Config{}, err
		}
		c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("Ignored unparseable credentials %s", CodexAuthPath(paths)))
	}
	c.ClaudeSubscriptionCredentials, err = LoadClaudeSubscriptionCredentials(paths)
	if err != nil {
		var syntax *sourceSyntaxError
		if !errors.As(err, &syntax) {
			return Config{}, err
		}
		c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("Ignored unparseable credentials %s", ClaudeSubscriptionAuthPath(paths)))
	}
	return c, nil
}

// WithSettings applies explicit runtime values to this snapshot using Viper's
// highest-priority Set layer. No file or environment is reread.
func (c Config) WithSettings(changes map[Setting]string) (Config, error) {
	values, err := settingsValues(c.settings())
	if err != nil {
		return Config{}, err
	}
	v := viper.New()
	if err := v.MergeConfigMap(values); err != nil {
		return Config{}, err
	}
	for key, value := range changes {
		if !mutableSetting(key) {
			return Config{}, fmt.Errorf("config: unsupported setting %q", key)
		}
		v.Set(string(key), strings.TrimSpace(value))
	}
	next, err := decodeEffective(v)
	if err != nil {
		return Config{}, err
	}
	next.Paths, next.CodexCredentials = c.Paths, c.CodexCredentials
	next.ClaudeSubscriptionCredentials = c.ClaudeSubscriptionCredentials
	next.Diagnostics, next.startupOverrides = c.Diagnostics, c.startupOverrides
	next.Web, next.webCredentials, next.webEnv = c.Web, c.webCredentials, c.webEnv
	return next, nil
}

// SavedValuesOverridden reports whether a startup layer above the user file
// supplies any changed key. It never exposes credential values.
func (c Config) SavedValuesOverridden(changes map[Setting]string) bool {
	for key := range changes {
		if _, ok := c.startupOverrides[string(key)]; ok {
			return true
		}
	}
	return false
}

func (c Config) settings() Settings {
	s := Settings{
		MaxTurns:            c.MaxTurns,
		RunNoProgressLimit:  c.RunNoProgressLimit,
		RunTokenBudget:      c.RunTokenBudget,
		RunTimeout:          c.RunTimeout.String(),
		Provider:            c.Provider,
		Model:               c.Model,
		Thinking:            c.Thinking,
		DefaultProjectTrust: c.DefaultProjectTrust,
		DeepSeekAPIKey:      c.DeepSeekAPIKey,
		DeepSeekBaseURL:     c.DeepSeekBaseURL,
		OpenCodeAPIKey:      c.OpenCodeAPIKey,
		OpenCodeBaseURL:     c.OpenCodeBaseURL,
		OpenAIAPIKey:        c.OpenAIAPIKey,
		OpenAIBaseURL:       c.OpenAIBaseURL,
		AnthropicAPIKey:     c.AnthropicAPIKey,
		AnthropicBaseURL:    c.AnthropicBaseURL,
		KimiAPIKey:          c.KimiAPIKey,
		KimiBaseURL:         c.KimiBaseURL,
		ZhipuCodingAPIKey:   c.ZhipuCodingAPIKey,
		ZhipuCodingBaseURL:  c.ZhipuCodingBaseURL,
		ZhipuAPIKey:         c.ZhipuAPIKey,
		ZhipuBaseURL:        c.ZhipuBaseURL,
		MoonshotAPIKey:      c.MoonshotAPIKey,
		MoonshotBaseURL:     c.MoonshotBaseURL,
		AiHubMixAPIKey:      c.AiHubMixAPIKey,
		AiHubMixBaseURL:     c.AiHubMixBaseURL,
		CustomAPIKey:        c.CustomAPIKey,
		CustomBaseURL:       c.CustomBaseURL,
		NoDepInstall:        c.NoDepInstall,
		NoUpdateCheck:       c.NoUpdateCheck,
		BrowserHeaded:       c.BrowserHeaded,
	}
	for key, tokens := range c.ContextWindows {
		provider, model, _ := strings.Cut(key, "/")
		s.ContextWindows = append(s.ContextWindows, ContextWindow{Provider: provider, Model: model, Tokens: tokens})
	}
	slices.SortFunc(s.ContextWindows, func(a, b ContextWindow) int { return strings.Compare(a.Provider+"/"+a.Model, b.Provider+"/"+b.Model) })
	return s
}

func decodeEffective(v *viper.Viper) (Config, error) {
	values := v.AllSettings()
	// AllSettings omits empty maps when flattening. Get each schema field
	// explicitly so an invalid winning object cannot disappear as "missing".
	for key := range EnvironmentVariables() {
		if value := v.Get(key); value != nil {
			values[key] = value
		}
	}
	for key, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		switch key {
		case "no_dep_install", "no_update_check", "browser_headed":
			parsed, err := strconv.ParseBool(text)
			if err != nil {
				return Config{}, fmt.Errorf("config: %s must be a boolean", key)
			}
			values[key] = parsed
		case "run_token_budget", "run_no_progress_limit", "max_turns":
			parsed, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				return Config{}, fmt.Errorf("config: %s must be an integer", key)
			}
			values[key] = parsed
		case "context_windows":
			var parsed []any
			if err := decodeValues([]byte(text), &parsed); err != nil {
				return Config{}, errors.New("config: context_windows must be a JSON array")
			}
			values[key] = parsed
		default:
			values[key] = text
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return Config{}, fmt.Errorf("config: encode effective values: %w", err)
	}
	var s Settings
	if err := jsonutil.DecodeStrict(data, &s); err != nil {
		// Decoder errors can quote secrets in invalid fields; report only schema context.
		var field *json.UnmarshalTypeError
		if errors.As(err, &field) {
			return Config{}, fmt.Errorf("config: invalid type for %s (expected %s)", field.Field, field.Type)
		}
		return Config{}, fmt.Errorf("config: invalid effective configuration: %w", err)
	}
	if err := s.validate(); err != nil {
		return Config{}, err
	}
	timeout, _ := time.ParseDuration(s.RunTimeout) // validated above
	c := Config{
		MaxTurns:            s.MaxTurns,
		RunNoProgressLimit:  s.RunNoProgressLimit,
		RunTokenBudget:      s.RunTokenBudget,
		RunTimeout:          timeout,
		Provider:            s.Provider,
		Model:               s.Model,
		Thinking:            s.Thinking,
		DefaultProjectTrust: s.DefaultProjectTrust,
		DeepSeekAPIKey:      s.DeepSeekAPIKey,
		DeepSeekBaseURL:     s.DeepSeekBaseURL,
		OpenCodeAPIKey:      s.OpenCodeAPIKey,
		OpenCodeBaseURL:     s.OpenCodeBaseURL,
		OpenAIAPIKey:        s.OpenAIAPIKey,
		OpenAIBaseURL:       s.OpenAIBaseURL,
		AnthropicAPIKey:     s.AnthropicAPIKey,
		AnthropicBaseURL:    s.AnthropicBaseURL,
		KimiAPIKey:          s.KimiAPIKey,
		KimiBaseURL:         s.KimiBaseURL,
		ZhipuCodingAPIKey:   s.ZhipuCodingAPIKey,
		ZhipuCodingBaseURL:  s.ZhipuCodingBaseURL,
		ZhipuAPIKey:         s.ZhipuAPIKey,
		ZhipuBaseURL:        s.ZhipuBaseURL,
		MoonshotAPIKey:      s.MoonshotAPIKey,
		MoonshotBaseURL:     s.MoonshotBaseURL,
		AiHubMixAPIKey:      s.AiHubMixAPIKey,
		AiHubMixBaseURL:     s.AiHubMixBaseURL,
		CustomAPIKey:        s.CustomAPIKey,
		CustomBaseURL:       s.CustomBaseURL,
		NoDepInstall:        s.NoDepInstall,
		NoUpdateCheck:       s.NoUpdateCheck,
		BrowserHeaded:       s.BrowserHeaded,
	}
	if len(s.ContextWindows) != 0 {
		c.ContextWindows = make(map[string]int64, len(s.ContextWindows))
	}
	for _, entry := range s.ContextWindows {
		c.ContextWindows[entry.Provider+"/"+entry.Model] = entry.Tokens
	}
	return c, nil
}

func settingsValues(s Settings) (map[string]any, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	err = decodeValues(data, &values)
	return values, err
}

func decodeValues(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}

type sourceSyntaxError struct{ path string }

func (e *sourceSyntaxError) Error() string { return "config: invalid JSON object in " + e.path }

func readValues(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var values map[string]any
	if err := decodeValues(data, &values); err != nil || values == nil {
		return nil, &sourceSyntaxError{path: path}
	}
	return values, nil
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
	return saveAPIKeyFile(paths, "DeepSeek", "deepseek_api_key", apiKey)
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
	return saveAPIKeyFile(paths, "OpenCode Go", "opencode_api_key", apiKey)
}

// SaveOpenAIAPIKey stores the OpenAI credential in the global auth file.
func SaveOpenAIAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveOpenAIAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveOpenAIAPIKeyFile stores the OpenAI credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveOpenAIAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: OpenAI API key is required")
	}
	return saveAPIKeyFile(paths, "OpenAI", "openai_api_key", apiKey)
}

// SaveAnthropicAPIKey stores the Anthropic credential in the global auth file.
func SaveAnthropicAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveAnthropicAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveAnthropicAPIKeyFile stores the Anthropic credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveAnthropicAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: Anthropic API key is required")
	}
	return saveAPIKeyFile(paths, "Anthropic", "anthropic_api_key", apiKey)
}

// SaveKimiAPIKey stores the Kimi credential in the global auth file.
func SaveKimiAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveKimiAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveKimiAPIKeyFile stores the Kimi credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveKimiAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: Kimi API key is required")
	}
	return saveAPIKeyFile(paths, "Kimi", "kimi_api_key", apiKey)
}

// SaveZhipuCodingAPIKey stores the Zhipu Coding Plan credential in the global auth file.
func SaveZhipuCodingAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveZhipuCodingAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveZhipuCodingAPIKeyFile stores the Zhipu Coding Plan credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveZhipuCodingAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: Zhipu Coding Plan API key is required")
	}
	return saveAPIKeyFile(paths, "Zhipu Coding Plan", "zhipu_coding_api_key", apiKey)
}

// SaveZhipuAPIKey stores the Zhipu credential in the global auth file.
func SaveZhipuAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveZhipuAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveZhipuAPIKeyFile stores the Zhipu credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveZhipuAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: Zhipu API key is required")
	}
	return saveAPIKeyFile(paths, "Zhipu", "zhipu_api_key", apiKey)
}

// SaveMoonshotAPIKey stores the Moonshot credential in the global auth file.
func SaveMoonshotAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveMoonshotAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveMoonshotAPIKeyFile stores the Moonshot credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveMoonshotAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: Moonshot API key is required")
	}
	return saveAPIKeyFile(paths, "Moonshot", "moonshot_api_key", apiKey)
}

// SaveAiHubMixAPIKey stores the AiHubMix credential in the global auth file.
func SaveAiHubMixAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveAiHubMixAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveAiHubMixAPIKeyFile stores the AiHubMix credential in an explicit global
// auth file, preserving any other provider credentials already present.
func SaveAiHubMixAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("config: AiHubMix API key is required")
	}
	return saveAPIKeyFile(paths, "AiHubMix", "aihubmix_api_key", apiKey)
}

// SaveCustomAPIKey stores the custom OpenAI-compatible credential in the global auth file.
func SaveCustomAPIKey(apiKey string) (string, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := SaveCustomAPIKeyFile(paths, apiKey); err != nil {
		return "", err
	}
	return paths.GlobalAuth, nil
}

// SaveCustomAPIKeyFile stores the custom credential in an explicit global
// auth file, preserving any other provider credentials already present.
// An empty key is allowed so keyless local servers (e.g. Ollama) can clear
// the entry; callers that require auth should validate separately.
func SaveCustomAPIKeyFile(paths Paths, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if strings.ContainsAny(apiKey, "\r\n") {
		return fmt.Errorf("config: Custom API key must be one line")
	}
	return saveAPIKeyFile(paths, "Custom", "custom_api_key", apiKey)
}

func (p Paths) validate() error {
	for name, path := range map[string]string{
		"global settings": p.GlobalSettings,
		"global auth":     p.GlobalAuth,
		"global trust":    p.GlobalTrust,
		"bin dir":         p.BinDir,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("config: %s path is required", name)
		}
	}
	return nil
}

func (s Settings) validate() error {
	if s.MaxTurns < 0 {
		return errors.New("config: max_turns cannot be negative")
	}
	if s.RunNoProgressLimit < 0 || s.RunNoProgressLimit == 1 {
		return errors.New("config: run_no_progress_limit must be zero or at least two")
	}
	if s.RunTokenBudget < 0 {
		return errors.New("config: run_token_budget cannot be negative")
	}
	if s.RunTimeout != "" {
		timeout, err := time.ParseDuration(s.RunTimeout)
		if err != nil || timeout < 0 {
			return errors.New("config: run_timeout must be a non-negative duration such as 30m")
		}
	}
	seen := make(map[string]bool)
	for _, entry := range s.ContextWindows {
		key := entry.Provider + "/" + entry.Model
		if entry.Provider == "" || strings.Contains(entry.Provider, "/") || entry.Model == "" || strings.ContainsAny(key, " \t\r\n") || entry.Tokens <= 0 || seen[key] {
			return fmt.Errorf("context_windows entry %q must identify a unique provider/model and a positive token count", key)
		}
		seen[key] = true
	}
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
	default:
		return fmt.Errorf("unsupported thinking level %q", s.Thinking)
	}
	switch s.DefaultProjectTrust {
	case "", trust.DefaultAsk, trust.DefaultAlways, trust.DefaultNever:
	default:
		return fmt.Errorf(
			"unsupported default project trust %q",
			s.DefaultProjectTrust,
		)
	}
	for key, endpoint := range map[string]string{
		"deepseek_base_url":     s.DeepSeekBaseURL,
		"opencode_base_url":     s.OpenCodeBaseURL,
		"openai_base_url":       s.OpenAIBaseURL,
		"anthropic_base_url":    s.AnthropicBaseURL,
		"kimi_base_url":         s.KimiBaseURL,
		"zhipu_coding_base_url": s.ZhipuCodingBaseURL,
		"zhipu_base_url":        s.ZhipuBaseURL,
		"moonshot_base_url":     s.MoonshotBaseURL,
		"aihubmix_base_url":     s.AiHubMixBaseURL,
		"custom_base_url":       s.CustomBaseURL,
	} {
		if err := validateCustomBaseURL(endpoint); err != nil {
			return fmt.Errorf("config: %s: %w", key, err)
		}
	}
	for key, credential := range map[string]string{
		"deepseek_api_key":     s.DeepSeekAPIKey,
		"opencode_api_key":     s.OpenCodeAPIKey,
		"openai_api_key":       s.OpenAIAPIKey,
		"anthropic_api_key":    s.AnthropicAPIKey,
		"kimi_api_key":         s.KimiAPIKey,
		"zhipu_coding_api_key": s.ZhipuCodingAPIKey,
		"zhipu_api_key":        s.ZhipuAPIKey,
		"moonshot_api_key":     s.MoonshotAPIKey,
		"aihubmix_api_key":     s.AiHubMixAPIKey,
		"custom_api_key":       s.CustomAPIKey,
	} {
		if strings.ContainsAny(credential, "\r\n") {
			return fmt.Errorf("config: %s must be one line", key)
		}
	}
	return nil
}

func validateCustomBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("custom base URL must not contain whitespace")
	}
	if !(strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")) {
		return fmt.Errorf("custom base URL must start with http:// or https://")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("base URL must have a valid host")
	}
	return nil
}
