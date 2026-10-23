// Package config loads and persists AICE process configuration.
package config

import (
	"bytes"
	"context"
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
	"github.com/ch1lam/aice-cli/internal/lockedfile"
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
	SettingDesktopEnabled     Setting = "desktop_enabled"
	SettingDesktopControlMode Setting = "desktop_control_mode"
	SettingBrowserHeaded      Setting = "browser_headed"
	SettingProvider           Setting = settingsKeyProvider
	SettingModel              Setting = settingsKeyModel
	SettingThinking           Setting = settingsKeyThinking
	SettingCustomBaseURL      Setting = settingsKeyCustomBaseURL
)

// ContextWindow identifies a model without treating its identifier as a Viper key.
type ContextWindow struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Tokens   int64  `json:"tokens"`
}

// Settings is the file schema shared by user and project configuration.
// Provider API keys and base URLs follow providerKeys and are decoded into
// Config maps; API keys normally live in auth.json with the same precedence.
type Settings struct {
	DesktopEnabled      bool               `json:"desktop_enabled,omitempty"`
	DesktopControlMode  DesktopControlMode `json:"desktop_control_mode,omitempty"`
	BrowserHeaded       bool               `json:"browser_headed,omitempty"`
	MaxTurns            int                `json:"max_turns,omitempty"`
	RunNoProgressLimit  int                `json:"run_no_progress_limit"`
	RunTokenBudget      int64              `json:"run_token_budget,omitempty"`
	RunTimeout          string             `json:"run_timeout,omitempty"`
	ContextWindows      []ContextWindow    `json:"context_windows,omitempty"`
	Provider            string             `json:"provider,omitempty"`
	Model               string             `json:"model,omitempty"`
	Thinking            llm.ThinkingLevel  `json:"thinking,omitempty"`
	DefaultProjectTrust trust.Default      `json:"default_project_trust,omitempty"`
	NoDepInstall        bool               `json:"no_dep_install,omitempty"`
	NoUpdateCheck       bool               `json:"no_update_check,omitempty"`
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
	DesktopEnabled      bool
	DesktopControlMode  DesktopControlMode
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
	// APIKeys and BaseURLs hold API-key provider settings keyed by provider
	// ID. Snapshots share these maps; change them only through WithAPIKey.
	APIKeys          map[string]string
	BaseURLs         map[string]string
	NoDepInstall     bool
	NoUpdateCheck    bool
	CodexCredentials CodexCredentials
	Web              WebConfig
	MCP              MCPConfig
	Paths            Paths
	Diagnostics      []string
	startupOverrides map[string]bool
	layers           *frozenSettings
	// Credential references resolve against the frozen environment after runtime
	// changes; Web and MCP never reread process environment during an edit.
	webCredentials    map[string]string
	environmentLookup func(string) (string, bool)
	webUser           *WebSettings
	mcpInputs         mcpLayer

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
	variables := map[string]string{
		"browser_headed":        "AICE_BROWSER_HEADED",
		"max_turns":             "AICE_MAX_TURNS",
		"run_no_progress_limit": "AICE_RUN_NO_PROGRESS_LIMIT",
		"run_token_budget":      "AICE_RUN_TOKEN_BUDGET", "run_timeout": "AICE_RUN_TIMEOUT",
		"provider": EnvProvider, "model": EnvModel, "thinking": EnvThinking,
		"default_project_trust": "AICE_DEFAULT_PROJECT_TRUST",
		"context_windows":       "AICE_CONTEXT_WINDOWS",
		"no_dep_install":        "AICE_NO_DEP_INSTALL", "no_update_check": "AICE_NO_UPDATE_CHECK",
	}
	providerEnvironmentVariables(variables)
	return variables
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
	for key, value := range defaultValues() {
		v.SetDefault(key, value)
	}
	var diagnostics []string
	var webValues webLayer
	var mcpValues mcpLayer
	layers := newFrozenSettings()
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
		values, err := readValues(context.Background(), path)
		if err != nil {
			var syntax *sourceSyntaxError
			if !errors.As(err, &syntax) {
				return Config{}, err
			}
			diagnostics = append(diagnostics, fmt.Sprintf("Ignored unparseable configuration %s", path))
			continue
		}
		// Nested service collections retain their identity outside Viper's deep merge.
		if err := mcpValues.extract(path, values, paths); err != nil {
			return Config{}, err
		}
		if err := webValues.extractWebValues(path, values, path == paths.ProjectSettings); err != nil {
			return Config{}, err
		}
		kind := "user-settings"
		if path == paths.GlobalAuth {
			kind = "user-auth"
		}
		if path == paths.ProjectSettings {
			kind = "project"
		}
		filterSettingSources(values, kind)
		layers.add(Source{Kind: kind, Location: path}, values)
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
	if err := layers.addInvocation(options); err != nil {
		return Config{}, err
	}
	// User-only fields are resolved from accepted frozen layers, overriding even
	// an embedding caller's unsupported flag/env bindings in Viper.
	for _, def := range SettingDefinitions() {
		if def.UserOnly {
			v.Set(string(def.ID), layers.resolved()[string(def.ID)])
		}
	}
	c, err := decodeEffective(v)
	if err != nil {
		return Config{}, err
	}
	c.Paths, c.Diagnostics = paths, diagnostics
	c.layers = layers
	if options.Environment {
		environment := make(map[string]string)
		for _, entry := range os.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			environment[key] = value
		}
		c.environmentLookup = func(key string) (string, bool) { value, ok := environment[key]; return value, ok }
	}
	c.webCredentials = webValues.credentials
	effectiveWebConfig, webDiagnostics, err := effectiveWeb(webValues, paths.ProjectSettings, c.environmentLookup)
	if err != nil {
		return Config{}, err
	}
	c.Web = effectiveWebConfig
	c.MCP, err = effectiveMCP(mcpValues, paths, c.environmentLookup)
	if err != nil {
		return Config{}, err
	}
	c.mcpInputs = mcpValues
	c.Diagnostics = append(c.Diagnostics, mcpValues.diagnostics...)
	webUser, err := decodeWebSettings(webValues.user)
	if err != nil {
		return Config{}, err
	}
	c.webUser = &webUser
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
	patch := SettingsPatch{}
	for key, text := range changes {
		if !mutableSetting(key) {
			return Config{}, fmt.Errorf("config: unsupported setting %q", key)
		}
		value, err := ParseSettingValue(key, text)
		if err != nil {
			return Config{}, err
		}
		patch.Changes = append(patch.Changes, SettingChange{ID: key, Value: value})
	}
	return c.WithPatch(patch)
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
		DesktopEnabled:      c.DesktopEnabled,
		DesktopControlMode:  c.DesktopControlMode,
		MaxTurns:            c.MaxTurns,
		RunNoProgressLimit:  c.RunNoProgressLimit,
		RunTokenBudget:      c.RunTokenBudget,
		RunTimeout:          c.RunTimeout.String(),
		Provider:            c.Provider,
		Model:               c.Model,
		Thinking:            c.Thinking,
		DefaultProjectTrust: c.DefaultProjectTrust,
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
		case "no_dep_install", "no_update_check", "browser_headed", "desktop_enabled":
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
	apiKeys, baseURLs, err := takeProviderValues(values)
	if err != nil {
		return Config{}, err
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
	if s.DesktopControlMode == "" {
		s.DesktopControlMode = DesktopBackgroundOnly
	}
	if err := s.validate(); err != nil {
		return Config{}, err
	}
	timeout, _ := time.ParseDuration(s.RunTimeout) // validated above
	c := Config{
		DesktopEnabled:      s.DesktopEnabled,
		DesktopControlMode:  s.DesktopControlMode,
		MaxTurns:            s.MaxTurns,
		RunNoProgressLimit:  s.RunNoProgressLimit,
		RunTokenBudget:      s.RunTokenBudget,
		RunTimeout:          timeout,
		Provider:            s.Provider,
		Model:               s.Model,
		Thinking:            s.Thinking,
		DefaultProjectTrust: s.DefaultProjectTrust,
		APIKeys:             apiKeys,
		BaseURLs:            baseURLs,
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

// values returns this snapshot's settings as flat configuration values.
func (c Config) values() (map[string]any, error) {
	values, err := settingsValues(c.settings())
	if err != nil {
		return nil, err
	}
	putProviderValues(values, c.APIKeys, c.BaseURLs)
	return values, nil
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

func readValues(ctx context.Context, path string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := lockedfile.Read(ctx, path)
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
	switch s.DesktopControlMode {
	case "", DesktopBackgroundOnly, DesktopForegroundAllowed:
	default:
		return errors.New("config: desktop_control_mode must be background_only or foreground_allowed")
	}
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
