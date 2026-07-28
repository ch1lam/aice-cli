package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func (s *interactiveSession) SlashCommands() []tui.SlashCommand {
	return []tui.SlashCommand{
		{
			Name:        "session",
			Description: "Show current Session information",
		},
		{
			Name:        "tree",
			Description: "Show all Session branches and the active leaf",
		},
		{
			Name:         "checkout",
			Description:  "Move the active leaf without deleting later branches",
			ArgumentHint: "<entry|root>",
		},
		{
			Name:        "compact",
			Description: "Compact the active branch at the current turn boundary",
		},
		{
			Name:        "settings",
			Description: "Show effective model settings and configuration paths",
		},
		{
			Name:         "login",
			Description:  "Store a provider API key using hidden input",
			ArgumentHint: "[provider]",
			SecretPrompt: "DeepSeek API key",
		},
		{
			Name:         "provider",
			Description:  "Select the provider and save it to one settings scope",
			ArgumentHint: "[--local] <provider>",
		},
		{
			Name:         "model",
			Description:  "Select the model and save it to one settings scope",
			ArgumentHint: "[--local] <model>",
		},
		{
			Name:         "thinking",
			Description:  "Set reasoning level; off disables thinking",
			ArgumentHint: "[--local] <level>",
		},
	}
}

func (s *interactiveSession) RunSlashCommand(
	ctx context.Context,
	request tui.SlashCommandRequest,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("app: context is required")
	}
	if s == nil {
		return "", fmt.Errorf("app: interactive Session is required")
	}

	switch request.Name {
	case "session":
		if s.store == nil {
			return "", fmt.Errorf("app: interactive Session store is required")
		}
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		return s.sessionInformation()
	case "tree":
		if s.store == nil {
			return "", fmt.Errorf("app: interactive Session store is required")
		}
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		snapshot, err := s.store.Snapshot()
		if err != nil {
			return "", fmt.Errorf("app: read Session tree: %w", err)
		}
		output := new(bytes.Buffer)
		if err := writeSessionTree(output, snapshot); err != nil {
			return "", err
		}
		return output.String(), nil
	case "checkout":
		if s.store == nil {
			return "", fmt.Errorf("app: interactive Session store is required")
		}
		entry, err := slashCommandEntry(request)
		if err != nil {
			return "", err
		}
		output := new(bytes.Buffer)
		if err := checkoutSessionStore(ctx, s.store, entry, output); err != nil {
			return "", err
		}
		if err := s.reloadHistory(); err != nil {
			return "", err
		}
		return output.String(), nil
	case "compact":
		if s.store == nil {
			return "", fmt.Errorf("app: interactive Session store is required")
		}
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		if s.application == nil {
			return "", fmt.Errorf("app: application is required")
		}
		output, err := s.application.compactSession(ctx, s.store)
		if err != nil {
			return "", err
		}
		if err := s.reloadHistory(); err != nil {
			return "", err
		}
		return output, nil
	case "settings":
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		return s.settingsInformation(), nil
	case "login":
		return s.login(request)
	case "provider":
		scope, value, err := scopedSettingValue(request)
		if err != nil {
			return "", err
		}
		if value != string(deepseek.ProviderID) {
			return "", fmt.Errorf(
				"app: unsupported provider %q; available: %s",
				value,
				deepseek.ProviderID,
			)
		}
		if err := s.saveSetting(
			scope,
			config.SettingProvider,
			value,
		); err != nil {
			return "", err
		}
		s.configuration.Provider = value
		return savedSettingMessage("provider", value, scope), nil
	case "model":
		scope, value, err := scopedSettingValue(request)
		if err != nil {
			return "", err
		}
		model, exists := deepseekModel(value)
		if !exists {
			return "", fmt.Errorf(
				"app: unsupported model %q; available: %s",
				value,
				strings.Join(deepseekModelIDs(), ", "),
			)
		}
		if err := s.saveSetting(
			scope,
			config.SettingModel,
			value,
		); err != nil {
			return "", err
		}
		s.configuration.Model = value
		s.model = model
		return savedSettingMessage("model", value, scope), nil
	case "thinking":
		scope, value, err := scopedSettingValue(request)
		if err != nil {
			return "", err
		}
		level := llm.ThinkingLevel(value)
		_, options, err := resolveModelSettings(config.Config{
			Provider: s.configuration.Provider,
			Model:    s.model.ID,
			Thinking: level,
		})
		if err != nil {
			return "", err
		}
		if err := s.saveSetting(
			scope,
			config.SettingThinking,
			value,
		); err != nil {
			return "", err
		}
		s.configuration.Thinking = level
		s.options.Thinking = options.Thinking
		return savedSettingMessage("thinking", value, scope), nil
	default:
		return "", fmt.Errorf("app: unsupported slash command /%s", request.Name)
	}
}

func (s *interactiveSession) RuntimeState() tui.RuntimeState {
	if s == nil {
		return tui.RuntimeState{}
	}
	return tui.RuntimeState{
		Model:            s.model,
		Thinking:         s.options.Thinking,
		APIKeyConfigured: s.configuration.DeepSeekAPIKey != "",
	}
}

func (s *interactiveSession) login(
	request tui.SlashCommandRequest,
) (string, error) {
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
	}
	if strings.TrimSpace(s.workspace) == "" {
		return "", fmt.Errorf("app: configuration workspace is required")
	}

	fields := strings.Fields(request.Arguments)
	if len(fields) > 1 {
		return "", fmt.Errorf("app: usage: /login [provider]")
	}
	provider := s.configuration.Provider
	if provider == "" {
		provider = string(deepseek.ProviderID)
	}
	if len(fields) == 1 {
		provider = fields[0]
	}
	if provider != string(deepseek.ProviderID) {
		return "", fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			deepseek.ProviderID,
		)
	}

	apiKey := strings.TrimSpace(request.Secret)
	if apiKey == "" {
		return "", fmt.Errorf("app: DeepSeek API key is required")
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return "", fmt.Errorf("app: DeepSeek API key must be one line")
	}

	configuration := s.configuration
	configuration.Provider = provider
	configuration.DeepSeekAPIKey = apiKey
	loop, err := s.application.newAgentLoop(configuration, s.tools)
	if err != nil {
		return "", err
	}
	path, err := s.application.dependencies.saveAPIKey(
		s.workspace,
		apiKey,
	)
	if err != nil {
		return "", fmt.Errorf("app: save DeepSeek API key: %w", err)
	}

	s.configuration = configuration
	s.loop = loop
	return "Saved DeepSeek API key to " + path + ". AICE is ready.", nil
}

func (s *interactiveSession) settingsInformation() string {
	thinking := string(s.options.Thinking)
	if s.options.Thinking == llm.ThinkingLevelUnknown {
		thinking = "default"
	}
	apiKey := "not configured"
	if s.configuration.DeepSeekAPIKey != "" {
		apiKey = "configured"
	}
	lines := []string{
		"Settings",
		"Provider: " + string(s.model.Provider),
		"Model: " + s.model.ID,
		"Thinking: " + thinking,
		"API key: " + apiKey,
	}
	if s.configuration.Paths.GlobalSettings != "" {
		lines = append(
			lines,
			"Global settings: "+s.configuration.Paths.GlobalSettings,
		)
	}
	if s.configuration.Paths.ProjectSettings != "" {
		lines = append(
			lines,
			"Project settings: "+s.configuration.Paths.ProjectSettings,
		)
	}
	if s.configuration.Paths.GlobalAuth != "" {
		lines = append(
			lines,
			"Global credentials: "+s.configuration.Paths.GlobalAuth,
		)
	}
	return strings.Join(lines, "\n")
}

func (s *interactiveSession) saveSetting(
	scope config.Scope,
	setting config.Setting,
	value string,
) error {
	if s.application == nil ||
		s.application.dependencies.saveSetting == nil {
		return fmt.Errorf("app: configuration persistence is unavailable")
	}
	if strings.TrimSpace(s.workspace) == "" {
		return fmt.Errorf("app: configuration workspace is required")
	}
	if err := s.application.dependencies.saveSetting(
		s.workspace,
		scope,
		setting,
		value,
	); err != nil {
		return fmt.Errorf(
			"app: save %s setting: %w",
			setting,
			err,
		)
	}
	return nil
}

func scopedSettingValue(
	request tui.SlashCommandRequest,
) (config.Scope, string, error) {
	fields := strings.Fields(request.Arguments)
	switch {
	case len(fields) == 1:
		return config.ScopeGlobal, fields[0], nil
	case len(fields) == 2 && fields[0] == "--local":
		return config.ScopeProject, fields[1], nil
	default:
		return "", "", fmt.Errorf(
			"app: usage: /%s [--local] <%s>",
			request.Name,
			request.Name,
		)
	}
}

func savedSettingMessage(
	name string,
	value string,
	scope config.Scope,
) string {
	return fmt.Sprintf(
		"Set %s to %s for the current Session and saved it to %s settings.",
		name,
		value,
		scope,
	)
}

func deepseekModel(id string) (llm.Model, bool) {
	for _, model := range deepseek.Models() {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

func deepseekModelIDs() []string {
	models := deepseek.Models()
	ids := make([]string, len(models))
	for index, model := range models {
		ids[index] = model.ID
	}
	return ids
}

func (s *interactiveSession) sessionInformation() (string, error) {
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return "", fmt.Errorf("app: read Session information: %w", err)
	}
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return "", fmt.Errorf("app: read Session nodes: %w", err)
	}
	leaf := snapshot.LeafID
	if leaf == "" {
		leaf = "root"
	}
	return fmt.Sprintf(
		"Session %s\nPath: %s\nActive leaf: %s\nNodes: %d\nTurns: %d\nCompactions: %d",
		snapshot.Header.ID,
		s.store.Path(),
		leaf,
		len(nodes),
		len(snapshot.Turns),
		len(snapshot.Compactions),
	), nil
}

func (s *interactiveSession) reloadHistory() error {
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return fmt.Errorf("app: reload Session snapshot: %w", err)
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		return fmt.Errorf("app: reload Session history: %w", err)
	}
	s.history = history
	return nil
}

func requireNoSlashCommandArguments(
	request tui.SlashCommandRequest,
) error {
	if request.Arguments == "" {
		return nil
	}
	return fmt.Errorf("app: /%s does not accept arguments", request.Name)
}

func slashCommandEntry(request tui.SlashCommandRequest) (string, error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) != 1 {
		return "", fmt.Errorf("app: usage: /checkout <entry|root>")
	}
	return fields[0], nil
}
