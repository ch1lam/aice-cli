package app

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func (s *interactiveSession) SlashCommands() []interaction.Command {
	return []interaction.Command{
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
			Name:        "init",
			Description: "Create or improve AGENTS.md in this workspace",
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

func (s *interactiveSession) loginProviderMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	return &interaction.CommandMenu{
		Title:   "Select provider",
		Options: providerOptions(s.providers, settings.configuration),
	}
}

// trustMenu lists the project trust choices for the active workspace.
func (s *interactiveSession) trustMenu() *interaction.CommandMenu {
	choices := trust.Choices(s.workspacePath)
	options := make([]interaction.CommandOption, 0, len(choices))
	for index, choice := range choices {
		options = append(options, interaction.CommandOption{
			Label:       choice.Label,
			Description: trustChoiceDescription(choice),
			Arguments:   strconv.Itoa(index),
		})
	}
	return &interaction.CommandMenu{
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

func (s *interactiveSession) providerMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	return &interaction.CommandMenu{
		Title:   "Select provider",
		Options: providerOptions(s.providers, settings.configuration),
	}
}

// providerOptions lists every known provider for /login and /provider,
// marking the active one and annotating providers whose credential is already
// available so users can tell a saved key apart from a missing one.
func providerOptions(
	providers []provider.Provider,
	configuration config.Config,
) []interaction.CommandOption {
	options := make([]interaction.CommandOption, 0, len(providers))
	for _, candidate := range providers {
		providerID := string(candidate.ProviderID())
		description := candidate.MenuDescription()
		if candidate.Configured(configuration) {
			description += " · credential saved"
		}
		options = append(options, interaction.CommandOption{
			Label:       candidate.Label(),
			Description: description,
			Arguments:   providerID,
			Current:     providerID == configuration.Provider,
		})
	}
	return options
}

func (s *interactiveSession) modelMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	providerID := activeProvider(settings.model, settings.configuration)
	models := modelsForProvider(s.providers, providerID)
	options := make([]interaction.CommandOption, 0, len(models))
	for _, model := range models {
		options = append(options, interaction.CommandOption{
			Label:       model.Name,
			Description: model.ID,
			Arguments:   model.ID,
			Current:     settings.model.ID == model.ID,
		})
	}
	return &interaction.CommandMenu{
		Title:   "Select model",
		Options: options,
	}
}

// activeProvider returns the provider whose catalog should drive /model and
// /login, defaulting to DeepSeek when nothing is configured yet.
func activeProvider(model llm.Model, configuration config.Config) string {
	if model.Provider != "" {
		return string(model.Provider)
	}
	providerID := configuration.Provider
	if providerID == "" {
		providerID = string(deepseek.ProviderID)
	}
	return providerID
}

// providerModel resolves the model the current Session should run after a
// provider change, preferring the configured model when it belongs to provider
// and falling back to that provider's default.
func providerModel(
	providers []provider.Provider,
	providerID, modelID string,
) llm.Model {
	if modelID != "" {
		if candidate, ok := modelForProvider(providers, providerID, modelID); ok {
			return candidate
		}
	}
	return providerDefaultModel(providers, providerID)
}

func (s *interactiveSession) thinkingMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	levels := llm.SupportedThinkingLevels(settings.model)
	options := make([]interaction.CommandOption, 0, len(levels))
	for _, level := range levels {
		label, description := thinkingLevelDescription(level)
		value := string(level)
		options = append(options, interaction.CommandOption{
			Label:       label,
			Description: description,
			Arguments:   value,
			Current:     settings.options.Thinking == level,
		})
	}
	return &interaction.CommandMenu{
		Title:   "Select reasoning level",
		Options: options,
	}
}

func thinkingLevelDescription(level llm.ThinkingLevel) (label, description string) {
	switch level {
	case llm.ThinkingLevelOff:
		return "Off", "Disable reasoning"
	case llm.ThinkingLevelMinimal:
		return "Minimal", "Use the smallest reasoning budget"
	case llm.ThinkingLevelLow:
		return "Low", "Use a low reasoning budget"
	case llm.ThinkingLevelMedium:
		return "Medium", "Balance reasoning depth and speed"
	case llm.ThinkingLevelHigh:
		return "High", "Use a high reasoning budget"
	case llm.ThinkingLevelXHigh:
		return "Extra high", "Use a very high reasoning budget"
	case llm.ThinkingLevelMax:
		return "Maximum", "Use the maximum reasoning budget"
	default:
		return string(level), ""
	}
}

func (s *interactiveSession) checkoutMenu() *interaction.CommandMenu {
	menu := &interaction.CommandMenu{
		Title: "Select Session entry",
		Options: []interaction.CommandOption{{
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
		menu.Options = append(menu.Options, interaction.CommandOption{
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
	request interaction.CommandRequest,
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
		changed, err := checkoutSessionStore(ctx, s.store, entry, output)
		if err != nil {
			return "", err
		}
		if err := s.reloadHistory(); err != nil {
			return "", err
		}
		if changed {
			s.stateMu.Lock()
			s.sessionChanged = true
			s.stateMu.Unlock()
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
	case "init":
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		return s.runInitCommand(ctx)
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
		if !supportedProvider(s.providers, value) {
			return "", fmt.Errorf(
				"app: unsupported provider %q; available: %s",
				value,
				strings.Join(knownProviders(s.providers), ", "),
			)
		}
		settings := s.settingsSnapshot()
		configuration := settings.configuration
		configuration.Provider = value
		loop, err := s.application.newAgentLoop(configuration, s.tools)
		if err != nil {
			return "", err
		}
		if err := s.saveSetting(config.SettingProvider, value); err != nil {
			return "", err
		}
		model := providerModel(s.providers, value, configuration.Model)
		// Persist the model when the stored one does not belong to the new
		// provider's catalog and was replaced by its default, so a later
		// restart resolves the same model instead of failing with an
		// unsupported-model error.
		if model.ID != configuration.Model {
			if err := s.saveSetting(config.SettingModel, model.ID); err != nil {
				return "", err
			}
		}
		configuration.Model = model.ID
		effective := clampedThinkingForModel(model, configuration.Thinking)
		// The settings transition is one critical section so a concurrent side
		// snapshot freezes a mutually consistent provider/model/thinking tuple.
		s.stateMu.Lock()
		s.configuration = configuration
		s.loop = loop
		s.model = model
		s.options.Thinking = effective
		s.stateMu.Unlock()
		return savedSettingMessage("provider", value), nil
	case "model":
		value, err := slashCommandSettingValue(request)
		if err != nil {
			return "", err
		}
		settings := s.settingsSnapshot()
		providerID := activeProvider(settings.model, settings.configuration)
		model, exists := modelForProvider(s.providers, providerID, value)
		if !exists {
			return "", fmt.Errorf(
				"app: unsupported model %q; available: %s",
				value,
				strings.Join(modelIDsForProvider(s.providers, providerID), ", "),
			)
		}
		effective := clampedThinkingForModel(
			model,
			settings.configuration.Thinking,
		)
		if err := s.saveSetting(config.SettingModel, value); err != nil {
			return "", err
		}
		// The settings transition is one critical section so a concurrent side
		// snapshot freezes a mutually consistent model/thinking pair.
		s.stateMu.Lock()
		s.configuration.Model = value
		s.model = model
		s.options.Thinking = effective
		s.stateMu.Unlock()
		return savedSettingMessage("model", value), nil
	case "thinking":
		value, err := slashCommandSettingValue(request)
		if err != nil {
			return "", err
		}
		level := llm.ThinkingLevel(value)
		settings := s.settingsSnapshot()
		_, options, err := resolveModelSettings(s.providers, config.Config{
			Provider: settings.configuration.Provider,
			Model:    settings.model.ID,
			Thinking: level,
		})
		if err != nil {
			return "", err
		}
		if err := s.saveSetting(config.SettingThinking, value); err != nil {
			return "", err
		}
		s.stateMu.Lock()
		s.configuration.Thinking = level
		s.options.Thinking = options.Thinking
		s.stateMu.Unlock()
		return savedSettingMessage("thinking", value), nil
	default:
		return "", fmt.Errorf("app: unsupported slash command /%s", request.Name)
	}
}

func (s *interactiveSession) RuntimeState() interaction.RuntimeState {
	if s == nil {
		return interaction.RuntimeState{}
	}
	usage := s.usageSnapshot()
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	state := interaction.RuntimeState{
		Model:            interaction.DisplayModel{ID: s.model.ID},
		Thinking:         interaction.DisplayThinking(s.options.Thinking),
		APIKeyConfigured: providerConfigured(s.providers, s.configuration),
		Usage:            usage,
		SessionChanged:   s.sessionChanged,
	}
	s.sessionChanged = false
	return state
}

func (s *interactiveSession) usageSnapshot() interaction.DisplayUsage {
	var total *llm.Usage
	if s.store != nil {
		if snapshot, err := s.store.Snapshot(); err == nil {
			usage := session.TotalUsage(snapshot)
			total = &usage
		}
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if total != nil {
		s.totalUsage = *total
	}
	return newDisplayUsage(s.totalUsage)
}

func (s *interactiveSession) login(
	request interaction.CommandRequest,
) (string, error) {
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
	}

	fields := strings.Fields(request.Arguments)
	if len(fields) > 3 {
		return "", fmt.Errorf("app: usage: /login [provider] [endpoint] [model]")
	}
	if len(fields) >= 2 && fields[0] != string(custom.ProviderID) {
		return "", fmt.Errorf("app: endpoint/model is only supported for provider %q", custom.ProviderID)
	}
	settings := s.settingsSnapshot()
	provider := settings.configuration.Provider
	if provider == "" {
		provider = string(deepseek.ProviderID)
	}
	if len(fields) >= 1 {
		provider = fields[0]
	}
	if !supportedProvider(s.providers, provider) {
		return "", fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			strings.Join(knownProviders(s.providers), ", "),
		)
	}

	// Custom endpoint/model may be supplied as: /login custom [endpoint] [model]
	// Endpoint "-" is a placeholder meaning "no endpoint, use default" when only model is set.
	customEndpoint := ""
	customModel := ""
	if provider == string(custom.ProviderID) {
		if len(fields) == 2 {
			arg := strings.TrimSpace(fields[1])
			if arg == "-" {
				customEndpoint = ""
			} else if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
				customEndpoint = arg
			} else {
				customModel = arg
			}
			if customEndpoint != "" && !(strings.HasPrefix(customEndpoint, "http://") || strings.HasPrefix(customEndpoint, "https://")) {
				return "", fmt.Errorf("app: custom endpoint must start with http:// or https://")
			}
		} else if len(fields) == 3 {
			endpointArg := strings.TrimSpace(fields[1])
			if endpointArg != "-" {
				customEndpoint = endpointArg
				if !(strings.HasPrefix(customEndpoint, "http://") || strings.HasPrefix(customEndpoint, "https://")) {
					return "", fmt.Errorf("app: custom endpoint must start with http:// or https://")
				}
			}
			customModel = strings.TrimSpace(fields[2])
		}
	}

	apiKey := strings.TrimSpace(request.Secret)
	// Allow the hidden input to carry both endpoint and key for the custom
	// provider: "http://url [apikey]" or just "http://url" . This keeps the
	// TUI path single-step while staying centralized in /login.
	if provider == string(custom.ProviderID) && apiKey != "" {
		secretFields := strings.Fields(apiKey)
		if len(secretFields) > 0 && (strings.HasPrefix(secretFields[0], "http://") || strings.HasPrefix(secretFields[0], "https://")) {
			if customEndpoint == "" {
				customEndpoint = secretFields[0]
			}
			if len(secretFields) > 1 {
				apiKey = strings.Join(secretFields[1:], " ")
			} else {
				apiKey = ""
			}
		}
	}
	// The custom provider is keyless by design (Ollama); an empty key clears
	// the stored credential and still enables the provider. Other providers
	// keep the strict requirement.
	if provider != string(custom.ProviderID) && apiKey == "" {
		return "", fmt.Errorf(
			"app: %s API key is required",
			providerLabel(s.providers, provider),
		)
	}
	if strings.ContainsAny(apiKey, "\r\n") {
		return "", fmt.Errorf(
			"app: %s API key must be one line",
			providerLabel(s.providers, provider),
		)
	}

	configuration := settings.configuration
	configuration.Provider = provider
	// Persist custom endpoint/model before building the loop so the new loop dials
	// the intended URL and the model is immediately available.
	if provider == string(custom.ProviderID) {
		if customEndpoint != "" {
			if err := s.saveSetting(config.SettingCustomBaseURL, customEndpoint); err != nil {
				return "", err
			}
			configuration.CustomBaseURL = customEndpoint
		}
		if customModel != "" {
			if strings.ContainsAny(customModel, " \t\r\n") {
				return "", fmt.Errorf("app: model must not contain whitespace")
			}
			configuration.Model = customModel
		}
	}
	findProvider(s.providers, provider).ApplyAPIKey(&configuration, apiKey)
	loop, err := s.application.newAgentLoop(configuration, s.tools)
	if err != nil {
		return "", err
	}
	path, err := s.application.dependencies.saveAPIKey(provider, apiKey)
	if err != nil {
		return "", fmt.Errorf(
			"app: save %s API key: %w",
			providerLabel(s.providers, provider),
			err,
		)
	}

	model := providerModel(s.providers, provider, configuration.Model)
	// Persist the provider selection so the login state survives a restart;
	// the credential itself lives in the global auth file. The effective model
	// is persisted only when the stored one is empty or does not belong to the
	// selected provider, so a later restart resolves the same model instead of
	// failing with an unsupported-model error.
	if err := s.saveSetting(config.SettingProvider, provider); err != nil {
		return "", err
	}
	if model.ID != configuration.Model {
		if err := s.saveSetting(config.SettingModel, model.ID); err != nil {
			return "", err
		}
	}
	configuration.Model = model.ID
	effective := clampedThinkingForModel(model, configuration.Thinking)
	s.stateMu.Lock()
	s.configuration = configuration
	s.loop = loop
	s.model = model
	s.options.Thinking = effective
	s.stateMu.Unlock()
	if provider == string(custom.ProviderID) {
		endpoint := strings.TrimSpace(configuration.CustomBaseURL)
		if endpoint == "" {
			endpoint = custom.DefaultBaseURL
		}
		if apiKey == "" {
			return fmt.Sprintf("Configured %s (endpoint %s, no API key). AICE is ready.", providerLabel(s.providers, provider), endpoint), nil
		}
		return fmt.Sprintf("Configured %s (endpoint %s) and saved API key to %s. AICE is ready.", providerLabel(s.providers, provider), endpoint, path), nil
	}
	return "Saved " + providerLabel(s.providers, provider) + " API key to " + path +
		". AICE is ready.", nil
}

// clampedThinkingForModel re-clamps the requested reasoning level to a
// model's capabilities. The requested level stays in the configuration so a
// switch back to a model that supports it restores the original request.
func clampedThinkingForModel(
	model llm.Model,
	requested llm.ThinkingLevel,
) llm.ThinkingLevel {
	if requested == llm.ThinkingLevelUnknown {
		requested = llm.DefaultThinkingLevel
	}
	return llm.ClampThinkingLevel(model, requested)
}

func (s *interactiveSession) settingsInformation() string {
	settings := s.settingsSnapshot()
	thinking := string(settings.options.Thinking)
	if settings.options.Thinking == llm.ThinkingLevelUnknown {
		thinking = "default"
	}
	apiKey := "not configured"
	if providerConfigured(s.providers, settings.configuration) {
		apiKey = "configured"
	}
	endpoint := strings.TrimSpace(settings.configuration.CustomBaseURL)
	if endpoint == "" {
		endpoint = custom.DefaultBaseURL
	}
	lines := []string{
		"Settings",
		"Provider: " + string(settings.model.Provider),
		"Model: " + settings.model.ID,
		"Thinking: " + thinking,
		"Thinking (requested): " + string(settings.configuration.Thinking),
		"API key: " + apiKey,
		"Custom endpoint: " + endpoint,
	}
	if settings.configuration.DefaultProjectTrust == "" {
		lines = append(lines, "Default project trust: ask")
	} else {
		lines = append(
			lines,
			"Default project trust: "+string(
				settings.configuration.DefaultProjectTrust,
			),
		)
	}
	lines = append(
		lines,
		"Project trust: "+trustDecisionLabel(s.trustDecision)+
			" ("+s.trustSource.String()+")",
	)
	if settings.configuration.Paths.GlobalSettings != "" {
		lines = append(
			lines,
			"Global settings: "+settings.configuration.Paths.GlobalSettings,
		)
	}
	if settings.configuration.Paths.GlobalAuth != "" {
		lines = append(
			lines,
			"Global credentials: "+settings.configuration.Paths.GlobalAuth,
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
	request interaction.CommandRequest,
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
	request interaction.CommandRequest,
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

// reloadHistory serializes with main interaction commits, rebuilds from the
// durable store without holding the in-memory lock, then publishes the complete
// replacement in one short critical section.
func (s *interactiveSession) reloadHistory() error {
	s.historySyncMu.Lock()
	defer s.historySyncMu.Unlock()
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return fmt.Errorf("app: reload Session snapshot: %w", err)
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		return fmt.Errorf("app: reload Session history: %w", err)
	}
	s.historyMu.Lock()
	s.history = history
	s.historyMu.Unlock()
	return nil
}

func requireNoSlashCommandArguments(
	request interaction.CommandRequest,
) error {
	if request.Arguments == "" {
		return nil
	}
	return fmt.Errorf("app: /%s does not accept arguments", request.Name)
}

func slashCommandEntry(request interaction.CommandRequest) (string, error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) != 1 {
		return "", fmt.Errorf("app: usage: /checkout <entry|root>")
	}
	return fields[0], nil
}
