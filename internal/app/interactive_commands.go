package app

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/trust"
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
			Name:        "checkout",
			Description: "Choose where the next Session branch starts",
			Menu:        s.checkoutMenu(),
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
			Name:        "trust",
			Description: "Save a project trust decision for future runs",
			Menu:        s.trustMenu(),
		},
		{
			Name:         "login",
			Description:  "Choose a provider and store its API key using hidden input",
			SecretPrompt: "API key",
			Menu:         s.loginProviderMenu(),
		},
		{
			Name:        "provider",
			Description: "Choose and save the global provider",
			Menu:        s.providerMenu(),
		},
		{
			Name:        "model",
			Description: "Choose and save the global model",
			Menu:        s.modelMenu(),
		},
		{
			Name:        "thinking",
			Description: "Choose and save the global reasoning level",
			Menu:        s.thinkingMenu(),
		},
	}
}

func (s *interactiveSession) loginProviderMenu() *tui.SlashCommandMenu {
	return &tui.SlashCommandMenu{
		Title:   "Select provider",
		Options: providerOptions(s.configuration.Provider),
	}
}

// trustMenu lists the project trust choices for the active workspace.
func (s *interactiveSession) trustMenu() *tui.SlashCommandMenu {
	choices := trust.Choices(s.workspacePath)
	options := make([]tui.SlashCommandOption, 0, len(choices))
	for index, choice := range choices {
		options = append(options, tui.SlashCommandOption{
			Label:       choice.Label,
			Description: trustChoiceDescription(choice),
			Arguments:   strconv.Itoa(index),
		})
	}
	return &tui.SlashCommandMenu{
		Title:   "Project trust",
		Options: options,
	}
}

func trustChoiceDescription(choice trust.Choice) string {
	if len(choice.Updates) == 0 {
		return "Applies to this Session only"
	}
	return "Saved to the global trust store"
}

func (s *interactiveSession) providerMenu() *tui.SlashCommandMenu {
	return &tui.SlashCommandMenu{
		Title:   "Select provider",
		Options: providerOptions(s.configuration.Provider),
	}
}

func providerOptions(current string) []tui.SlashCommandOption {
	return []tui.SlashCommandOption{
		{
			Label:       "DeepSeek",
			Description: "DeepSeek API (V4 Flash via OpenAI Responses, V4 Pro via Anthropic Messages)",
			Arguments:   string(deepseek.ProviderID),
			Current:     current == string(deepseek.ProviderID),
		},
		{
			Label:       "OpenCode Go",
			Description: "OpenCode Go subscription (24 models via OpenAI Chat Completions)",
			Arguments:   string(opencode.ProviderID),
			Current:     current == string(opencode.ProviderID),
		},
	}
}

func (s *interactiveSession) modelMenu() *tui.SlashCommandMenu {
	provider := s.activeProvider()
	models := modelsForProvider(provider)
	options := make([]tui.SlashCommandOption, 0, len(models))
	for _, model := range models {
		options = append(options, tui.SlashCommandOption{
			Label:       model.Name,
			Description: model.ID,
			Arguments:   model.ID,
			Current:     s.model.ID == model.ID,
		})
	}
	return &tui.SlashCommandMenu{
		Title:   "Select model",
		Options: options,
	}
}

// activeProvider returns the provider whose catalog should drive /model and
// /login, defaulting to DeepSeek when nothing is configured yet.
func (s *interactiveSession) activeProvider() string {
	if s.model.Provider != "" {
		return string(s.model.Provider)
	}
	provider := s.configuration.Provider
	if provider == "" {
		provider = string(deepseek.ProviderID)
	}
	return provider
}

// providerModel resolves the model the current Session should run after a
// provider change, preferring the configured model when it belongs to provider
// and falling back to that provider's default.
func providerModel(provider, modelID string) llm.Model {
	if modelID != "" {
		if candidate, ok := modelForProvider(provider, modelID); ok {
			return candidate
		}
	}
	return providerDefaultModel(provider)
}

func (s *interactiveSession) thinkingMenu() *tui.SlashCommandMenu {
	levels := []struct {
		level       llm.ThinkingLevel
		label       string
		description string
	}{
		{
			level:       llm.ThinkingLevelOff,
			label:       "Off",
			description: "Disable reasoning",
		},
		{
			level:       llm.ThinkingLevelMinimal,
			label:       "Minimal",
			description: "Use the smallest reasoning budget",
		},
		{
			level:       llm.ThinkingLevelLow,
			label:       "Low",
			description: "Use a low reasoning budget",
		},
		{
			level:       llm.ThinkingLevelMedium,
			label:       "Medium",
			description: "Balance reasoning depth and speed",
		},
		{
			level:       llm.ThinkingLevelHigh,
			label:       "High",
			description: "Use a high reasoning budget",
		},
		{
			level:       llm.ThinkingLevelXHigh,
			label:       "Extra high",
			description: "Use a very high reasoning budget",
		},
		{
			level:       llm.ThinkingLevelMax,
			label:       "Maximum",
			description: "Use the maximum reasoning budget",
		},
	}
	options := make([]tui.SlashCommandOption, 0, len(levels))
	for _, option := range levels {
		value := string(option.level)
		options = append(options, tui.SlashCommandOption{
			Label:       option.label,
			Description: option.description,
			Arguments:   value,
			Current:     s.options.Thinking == option.level,
		})
	}
	return &tui.SlashCommandMenu{
		Title:   "Select reasoning level",
		Options: options,
	}
}

func (s *interactiveSession) checkoutMenu() *tui.SlashCommandMenu {
	menu := &tui.SlashCommandMenu{
		Title: "Select Session entry",
		Options: []tui.SlashCommandOption{{
			Label:       "Session root",
			Description: "Start the next branch from the beginning",
			Arguments:   "root",
		}},
	}
	if s.store == nil {
		return menu
	}

	snapshot, err := s.store.Snapshot()
	if err != nil {
		return menu
	}
	menu.Options[0].Current = snapshot.LeafID == ""
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return menu
	}
	turns := make(map[string]session.Turn, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		turns[turn.ID] = turn
	}
	compactions := make(map[string]session.Compaction, len(snapshot.Compactions))
	for _, compaction := range snapshot.Compactions {
		compactions[compaction.ID] = compaction
	}
	for _, node := range nodes {
		description := sessionNodeDescription(node, turns, compactions)
		if description == "" {
			description = "Session " + string(node.Type)
		}
		menu.Options = append(menu.Options, tui.SlashCommandOption{
			Label:       string(node.Type) + " " + shortSessionID(node.ID),
			Description: description,
			Arguments:   node.ID,
			Current:     node.ID == snapshot.LeafID,
		})
	}
	return menu
}

func shortSessionID(id string) string {
	const visible = 10
	if len(id) <= visible {
		return id
	}
	return id[:visible]
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
	case "trust":
		if s.trustStore == nil {
			return "", fmt.Errorf("app: trust store is required")
		}
		choice, err := slashCommandTrustChoice(
			request,
			trust.Choices(s.workspacePath),
		)
		if err != nil {
			return "", err
		}
		if len(choice.Updates) > 0 {
			if err := s.trustStore.SetMany(choice.Updates); err != nil {
				return "", fmt.Errorf("app: save project trust: %w", err)
			}
			return trustResultMessage(choice, true), nil
		}
		return trustResultMessage(choice, false), nil
	case "login":
		return s.login(request)
	case "provider":
		value, err := slashCommandSettingValue(request)
		if err != nil {
			return "", err
		}
		if !supportedProvider(value) {
			return "", fmt.Errorf(
				"app: unsupported provider %q; available: %s",
				value,
				strings.Join(knownProviders(), ", "),
			)
		}
		configuration := s.configuration
		configuration.Provider = value
		loop, err := s.application.newAgentLoop(configuration, s.tools)
		if err != nil {
			return "", err
		}
		if err := s.saveSetting(config.SettingProvider, value); err != nil {
			return "", err
		}
		s.configuration = configuration
		s.loop = loop
		s.model = providerModel(value, configuration.Model)
		return savedSettingMessage("provider", value), nil
	case "model":
		value, err := slashCommandSettingValue(request)
		if err != nil {
			return "", err
		}
		model, exists := modelForProvider(s.activeProvider(), value)
		if !exists {
			return "", fmt.Errorf(
				"app: unsupported model %q; available: %s",
				value,
				strings.Join(modelIDsForProvider(s.activeProvider()), ", "),
			)
		}
		if err := s.saveSetting(config.SettingModel, value); err != nil {
			return "", err
		}
		s.configuration.Model = value
		s.model = model
		return savedSettingMessage("model", value), nil
	case "thinking":
		value, err := slashCommandSettingValue(request)
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
		if err := s.saveSetting(config.SettingThinking, value); err != nil {
			return "", err
		}
		s.configuration.Thinking = level
		s.options.Thinking = options.Thinking
		return savedSettingMessage("thinking", value), nil
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
		APIKeyConfigured: providerConfigured(s.configuration),
	}
}

func (s *interactiveSession) login(
	request tui.SlashCommandRequest,
) (string, error) {
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
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
	if !supportedProvider(provider) {
		return "", fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			strings.Join(knownProviders(), ", "),
		)
	}

	apiKey := strings.TrimSpace(request.Secret)
	if apiKey == "" {
		return "", fmt.Errorf("app: %s API key is required", providerLabel(provider))
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return "", fmt.Errorf(
			"app: %s API key must be one line",
			providerLabel(provider),
		)
	}

	configuration := s.configuration
	configuration.Provider = provider
	switch provider {
	case string(opencode.ProviderID):
		configuration.OpenCodeAPIKey = apiKey
	default:
		configuration.DeepSeekAPIKey = apiKey
	}
	loop, err := s.application.newAgentLoop(configuration, s.tools)
	if err != nil {
		return "", err
	}
	path, err := s.application.dependencies.saveAPIKey(provider, apiKey)
	if err != nil {
		return "", fmt.Errorf(
			"app: save %s API key: %w",
			providerLabel(provider),
			err,
		)
	}

	s.configuration = configuration
	s.loop = loop
	s.model = providerModel(provider, configuration.Model)
	return "Saved " + providerLabel(provider) + " API key to " + path +
		". AICE is ready.", nil
}

func (s *interactiveSession) settingsInformation() string {
	thinking := string(s.options.Thinking)
	if s.options.Thinking == llm.ThinkingLevelUnknown {
		thinking = "default"
	}
	apiKey := "not configured"
	if providerConfigured(s.configuration) {
		apiKey = "configured"
	}
	lines := []string{
		"Settings",
		"Provider: " + string(s.model.Provider),
		"Model: " + s.model.ID,
		"Thinking: " + thinking,
		"API key: " + apiKey,
	}
	if s.configuration.DefaultProjectTrust == "" {
		lines = append(lines, "Default project trust: ask")
	} else {
		lines = append(
			lines,
			"Default project trust: "+string(s.configuration.DefaultProjectTrust),
		)
	}
	lines = append(
		lines,
		"Project trust: "+trustDecisionLabel(s.trustDecision)+
			" ("+s.trustSource.String()+")",
	)
	if s.configuration.Paths.GlobalSettings != "" {
		lines = append(
			lines,
			"Global settings: "+s.configuration.Paths.GlobalSettings,
		)
	}
	if s.configuration.Paths.GlobalAuth != "" {
		lines = append(
			lines,
			"Global credentials: "+s.configuration.Paths.GlobalAuth,
		)
	}
	if s.trustStore != nil && s.trustStore.Path() != "" {
		lines = append(lines, "Trust store: "+s.trustStore.Path())
	}
	return strings.Join(lines, "\n")
}

func trustDecisionLabel(decision trust.Decision) string {
	switch decision {
	case trust.DecisionTrusted:
		return "trusted"
	case trust.DecisionUntrusted:
		return "not trusted"
	default:
		return "unknown"
	}
}

// slashCommandTrustChoice resolves the selected trust choice from the menu
// option's numeric argument.
func slashCommandTrustChoice(
	request tui.SlashCommandRequest,
	choices []trust.Choice,
) (trust.Choice, error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) != 1 {
		return trust.Choice{}, fmt.Errorf("app: usage: /trust <choice>")
	}
	index, err := strconv.Atoi(fields[0])
	if err != nil || index < 0 || index >= len(choices) {
		return trust.Choice{}, fmt.Errorf(
			"app: invalid trust choice %q",
			fields[0],
		)
	}
	return choices[index], nil
}

func trustResultMessage(choice trust.Choice, persisted bool) string {
	suffix := "Restart AICE for the new trust state to affect prompt loading."
	if !persisted {
		return "Trust decision applied to this Session only. " + suffix
	}
	return "Trust decision saved. " + suffix
}

func (s *interactiveSession) saveSetting(
	setting config.Setting,
	value string,
) error {
	if s.application == nil ||
		s.application.dependencies.saveSetting == nil {
		return fmt.Errorf("app: configuration persistence is unavailable")
	}
	if err := s.application.dependencies.saveSetting(
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

func slashCommandSettingValue(
	request tui.SlashCommandRequest,
) (string, error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) != 1 {
		return "", fmt.Errorf(
			"app: usage: /%s <%s>",
			request.Name,
			request.Name,
		)
	}
	return fields[0], nil
}

func savedSettingMessage(
	name string,
	value string,
) string {
	return fmt.Sprintf(
		"Set %s to %s for the current Session and saved it to global settings.",
		name,
		value,
	)
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
