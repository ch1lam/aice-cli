package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func (s *interactiveSession) SlashCommands() []interaction.Command {
	commands := []interaction.Command{
		{Name: "usage", Description: "Open recorded Session usage", Panel: &interaction.Panel{View: interaction.PanelUsage, Tab: 1}},
		{Name: "context", Description: "Open current context information", Panel: &interaction.Panel{View: interaction.PanelUsage}},
		{Name: "history", Description: "Browse conversation history in this project", ArgumentHint: "[id]", Panel: &interaction.Panel{View: interaction.PanelHistory}},
		{Name: "browser", Description: "Set up browser automation as the user:browser MCP service", Menu: s.browserMenu(), Interactive: true},
		{Name: "web", Description: "Configure web search services, priority and web fetch", Menu: s.webMenu(), Interactive: true},
		{Name: "mcp", Description: "Manage MCP services, connection approval and credentials", ArgumentHint: "[action] [service]", Interactive: true,
			Panel: &interaction.Panel{View: interaction.PanelSettings, Focus: "mcp"}},
		{Name: "desktop", Description: "Open Computer Use in Settings", Panel: &interaction.Panel{View: interaction.PanelSettings, Focus: "desktop.setup", FocusAction: true}},
		{
			Name:        "session",
			Description: "Show current Session information",
			Panel:       &interaction.Panel{View: interaction.PanelUsage, Tab: 2},
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
			Description: "Compact older context on the active branch",
		},
		{
			Name:        "new",
			Description: "Detach from the current Session; the next prompt starts fresh",
		},
		{
			Name:        "init",
			Description: "Create or improve AGENTS.md in this workspace",
		},
		{
			Name:        "settings",
			Description: "Open Settings (Ctrl+,)",
			Panel:       &interaction.Panel{View: interaction.PanelSettings},
		},
		{
			Name:        "skills",
			Description: "Show discovered Agent Skills",
		},
		{
			Name:        "trust",
			Description: "Save a project trust decision for future runs",
			Menu:        s.trustMenu(),
		},
		{
			Name:         "login",
			Description:  "Choose a provider and configure its credentials",
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
	return append(commands, skillCommands(s.skills)...)
}

func (s *interactiveSession) loginProviderMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	var accounts, keys []interaction.CommandOption
	for _, option := range loginProviderOptions(s.providers, settings.configuration) {
		if option.Arguments == string(codex.ProviderID) || option.Arguments == string(claudesubscription.ProviderID) {
			accounts = append(accounts, option)
		} else {
			keys = append(keys, option)
		}
	}
	return &interaction.CommandMenu{
		Title: "Select authentication method",
		Options: []interaction.CommandOption{
			{Label: "Sign in with an account", Menu: &interaction.CommandMenu{Title: "Select account provider", Options: accounts}},
			{Label: "Sign in with an API key", Menu: &interaction.CommandMenu{Title: "Select API key provider", Options: keys}},
		},
	}
}

// trustMenu lists saved trust choices. Temporary startup choices cannot
// change the already-loaded project context, so they are not offered here.
func (s *interactiveSession) trustMenu() *interaction.CommandMenu {
	choices := trust.Choices(s.workspacePath)
	options := make([]interaction.CommandOption, 0, len(choices))
	for index, choice := range choices {
		if len(choice.Updates) == 0 {
			continue
		}
		options = append(options, interaction.CommandOption{
			Label:       choice.Label,
			Description: "Saved for future runs; restart AICE to apply",
			Arguments:   strconv.Itoa(index),
		})
	}
	return &interaction.CommandMenu{
		Title:   "Project trust",
		Options: options,
	}
}

func (s *interactiveSession) providerMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot()
	return &interaction.CommandMenu{
		Title:   "Select provider",
		Options: providerOptions(s.providers, settings.configuration),
	}
}

// providerOptions lists every known provider for /provider, marking the active
// one and annotating providers whose credential is already available.
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

// loginProviderOptions lists every known provider for /login. Providers with a
// configured credential open a second menu so switching and replacing a key
// remain explicit, separate actions.
func loginProviderOptions(
	providers []provider.Provider,
	configuration config.Config,
) []interaction.CommandOption {
	options := providerOptions(providers, configuration)
	for index, candidate := range providers {
		if candidate.ProviderID() == claudesubscription.ProviderID {
			methods := []interaction.CommandOption{{Label: "Browser login", Arguments: string(claudesubscription.ProviderID), LoginMethod: "browser"}}
			if candidate.Configured(configuration) {
				methods = append(methods, interaction.CommandOption{Label: "Use saved credential", Arguments: string(claudesubscription.ProviderID), UseSavedCredential: true})
			}
			options[index].Menu = &interaction.CommandMenu{Title: "Claude subscription login", Options: methods}
			continue
		}
		if candidate.ProviderID() == codex.ProviderID {
			methods := []interaction.CommandOption{
				{Label: "Browser login (default)", Arguments: string(codex.ProviderID), LoginMethod: "browser"},
				{Label: "Device code login (headless)", Arguments: string(codex.ProviderID), LoginMethod: "device-code"},
			}
			if candidate.Configured(configuration) {
				methods = append(methods, interaction.CommandOption{
					Label: "Use saved credential", Arguments: string(codex.ProviderID), UseSavedCredential: true,
				})
			}
			options[index].Menu = &interaction.CommandMenu{Title: "Select OpenAI Codex login method", Options: methods}
			continue
		}
		if !candidate.Configured(configuration) {
			continue
		}

		providerID := string(candidate.ProviderID())
		label := candidate.Label()
		options[index].Menu = &interaction.CommandMenu{
			Title: label + " credential",
			Options: []interaction.CommandOption{
				{
					Label:              "Use saved credential",
					Description:        "Switch to " + label + " without entering a key",
					Arguments:          providerID,
					UseSavedCredential: true,
				},
				{
					Label:       "Enter a new API key",
					Description: "Replace the saved " + label + " credential",
					Arguments:   providerID,
				},
			},
		}
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
	if s.conversation.store == nil {
		return menu
	}

	snapshot, err := s.conversation.store.Snapshot()
	if err != nil {
		return menu
	}
	menu.Options[0].Current = snapshot.LeafID == ""
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return menu
	}
	messages := make(map[string]session.MessageEntry, len(snapshot.Messages))
	for _, entry := range snapshot.Messages {
		messages[entry.ID] = entry
	}
	compactions := make(map[string]session.Compaction, len(snapshot.Compactions))
	for _, compaction := range snapshot.Compactions {
		compactions[compaction.ID] = compaction
	}
	for _, node := range nodes {
		target := snapshot
		target.LeafID = node.ID
		if _, err := session.BuildContext(target); err != nil {
			continue
		}
		description := sessionNodeDescription(node, messages, compactions)
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

type slashCommandHandler func(
	*interactiveSession,
	context.Context,
	interaction.CommandRequest,
) (string, error)

var slashCommandHandlers = map[string]slashCommandHandler{
	"history":  (*interactiveSession).slashResume,
	"usage":    (*interactiveSession).slashPanel,
	"context":  (*interactiveSession).slashPanel,
	"session":  (*interactiveSession).slashPanel,
	"tree":     (*interactiveSession).slashTree,
	"checkout": (*interactiveSession).slashCheckout,
	"compact":  (*interactiveSession).slashCompact,
	"new":      (*interactiveSession).slashNew,
	"init":     (*interactiveSession).slashInit,
	"settings": (*interactiveSession).slashPanel,
	"desktop":  (*interactiveSession).slashPanel,
	"skills":   (*interactiveSession).slashSkills,
	"trust":    (*interactiveSession).slashTrust,
	"provider": (*interactiveSession).slashProvider,
	"model":    (*interactiveSession).slashModel,
	"thinking": (*interactiveSession).slashThinking,
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
	case "mcp", "browser", "web", "login":
		// Slash entries share the Settings actions without a draft revision.
		result, err := s.settingsAction(ctx, nil, request)
		output := result.Output
		if len(result.Details) > 0 {
			output = strings.TrimSpace(output + "\n" + interaction.DetailsText(result.Details))
		}
		return appendWarnings(output, result.Warnings), err
	}
	handler, ok := slashCommandHandlers[request.Name]
	if !ok {
		return "", fmt.Errorf("app: unsupported slash command /%s", request.Name)
	}
	changes, shared := slashChangesResources(request)
	if !changes {
		return handler(s, ctx, request)
	}
	if err := s.beginSettingsOperation(nil, shared); err != nil {
		return "", err
	}
	output, err := handler(s, ctx, request)
	changed := err == nil
	_, warnings := s.endSettingsOperation(changed, changed && shared)
	return appendWarnings(output, warnings), err
}

func (s *interactiveSession) requireSessionStore() error {
	if s.conversation.store == nil {
		return fmt.Errorf("app: no session started yet; send your first prompt to begin")
	}
	return nil
}

// slashPanel rejects panel commands that reach the application. Interactive
// frontends open the panel for the bare command, so only arguments arrive.
func (s *interactiveSession) slashPanel(
	_ context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	return "", fmt.Errorf("app: /%s is shown in its panel", request.Name)
}

func (s *interactiveSession) slashTree(
	_ context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := s.requireSessionStore(); err != nil {
		return "", err
	}
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	snapshot, err := s.conversation.store.Snapshot()
	if err != nil {
		return "", fmt.Errorf("app: read Session tree: %w", err)
	}
	output := new(bytes.Buffer)
	if err := writeSessionTree(output, snapshot); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (s *interactiveSession) slashCheckout(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := s.requireSessionStore(); err != nil {
		return "", err
	}
	entry, err := slashCommandEntry(request)
	if err != nil {
		return "", err
	}
	output := new(bytes.Buffer)
	changed, err := checkoutSessionStore(ctx, s.conversation.store, entry, output)
	if err != nil {
		return "", err
	}
	if err := s.conversation.reloadHistory(); err != nil {
		return "", err
	}
	if changed {
		snapshot, err := s.conversation.store.Snapshot()
		if err != nil {
			return "", err
		}
		view, err := sessionTranscript(snapshot)
		if err != nil {
			return "", err
		}
		s.publishTranscript(view)
	}
	return output.String(), nil
}

func (s *interactiveSession) slashCompact(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := s.requireSessionStore(); err != nil {
		return "", err
	}
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
	}
	settings := s.settingsSnapshot()
	if settings.modelErr != nil {
		return "", settings.modelErr
	}
	configured := configuredModel{
		configuration: settings.configuration,
		model:         settings.model,
		options:       settings.options,
	}
	output, err := s.application.compactSession(ctx, s.conversation.store, &configured)
	if err != nil {
		return "", err
	}
	if err := s.conversation.reloadHistory(); err != nil {
		return "", err
	}
	return output, nil
}

// slashNew detaches from the current Session without creating a file: the
// next accepted prompt starts a fresh one through ensureSessionStore. The
// previous file is left untouched when it recorded messages; a previous file
// that never recorded anything is removed instead of lingering as a
// header-only stub. The TUI discards the visible transcript through the
// same sessionChanged channel as /checkout.
func (s *interactiveSession) slashNew(
	ctx context.Context,
	request interaction.CommandRequest,
) (output string, returnErr error) {
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	s.conversation.historyMu.RLock()
	active := s.conversation.activeMainRun != nil
	s.conversation.historyMu.RUnlock()
	if active {
		return "", fmt.Errorf("app: cannot start a new Session while a response is running")
	}
	// Serialize with message appends the same way reloadHistory does. The
	// active-run check above makes a concurrent commit impossible through
	// the TUI, which only submits slash commands while idle.
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()
	previous := s.conversation.store
	s.conversation.store = nil
	s.conversation.historyLeaf, s.conversation.historyReady = "", false
	s.guard.ResetSessionGrants()
	s.conversation.historyMu.Lock()
	s.conversation.history = nil
	s.conversation.historyMu.Unlock()
	s.stateMu.Lock()
	s.totalUsage = llm.Usage{}
	s.sessionChanged = true
	s.stateMu.Unlock()
	if previous == nil {
		return "Started new session", nil
	}
	previousPath := previous.Path()
	info, err := previous.Info()
	if err != nil {
		return "", errors.Join(
			fmt.Errorf("app: read previous session: %w", err),
			previous.Close(),
		)
	}
	if err := previous.Close(); err != nil {
		return "", err
	}
	if !info.HasRecords {
		if err := os.Remove(previousPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("app: remove empty session: %w", err)
		}
		return "Started new session", nil
	}
	return "Started new session\nPrevious session preserved: " + previousPath, nil
}

func (s *interactiveSession) slashInit(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	return s.runInitCommand(ctx)
}

func (s *interactiveSession) slashSkills(
	_ context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := requireNoSlashCommandArguments(request); err != nil {
		return "", err
	}
	return formatSkillsCommand(s.skills, s.skillDiags, s.workspacePath), nil
}

const savedProjectTrustMessage = "Trust decision saved. Restart AICE for the new trust state to affect project configuration, prompts, and Skills."

func (s *interactiveSession) slashTrust(
	_ context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if err := s.saveProjectTrustSelection(request.Arguments); err != nil {
		return "", err
	}
	return savedProjectTrustMessage, nil
}

func (s *interactiveSession) slashProvider(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	value, err := settingSelectionValue(request.Name, request.Arguments)
	if err != nil {
		return "", err
	}
	overridden, err := s.selectProvider(ctx, value)
	if err != nil {
		return "", err
	}
	return savedSettingMessage("provider", value, overridden), nil
}

func (s *interactiveSession) slashModel(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	value, err := settingSelectionValue(request.Name, request.Arguments)
	if err != nil {
		return "", err
	}
	overridden, err := s.selectModel(ctx, value)
	if err != nil {
		return "", err
	}
	return savedSettingMessage("model", value, overridden), nil
}

func (s *interactiveSession) slashThinking(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	value, err := settingSelectionValue(request.Name, request.Arguments)
	if err != nil {
		return "", err
	}
	overridden, err := s.selectThinking(ctx, value)
	if err != nil {
		return "", err
	}
	return savedSettingMessage("thinking", value, overridden), nil
}

func (s *interactiveSession) RuntimeState() interaction.RuntimeState {
	if s == nil {
		return interaction.RuntimeState{}
	}
	usage := s.usageSnapshot()
	sessionID := ""
	if s.conversation.store != nil {
		if info, err := s.conversation.store.Info(); err == nil {
			sessionID = info.Header.ID
		}
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	thinking, err := displayThinking(s.options.Thinking)
	if err != nil {
		// RuntimeState cannot fail the TUI refresh; fall back to the empty
		// default rather than inventing a third enum.
		thinking = interaction.DisplayThinkingDefault
	}
	state := interaction.RuntimeState{
		SessionID:        sessionID,
		Model:            interaction.DisplayModel{ID: s.model.ID},
		Thinking:         thinking,
		APIKeyConfigured: providerConfigured(s.providers, s.configuration),
		PermissionMode:   s.permissionMode(),
		Usage:            usage,
		Context:          s.contextSnapshotFor(s.model, s.configuration, s.systemPrompt),
		SessionChanged:   s.sessionChanged,
		Transcript:       s.transcript,
	}
	s.sessionChanged = false
	s.transcript = nil
	return state
}

func (s *interactiveSession) usageSnapshot() interaction.DisplayUsage {
	var total *llm.Usage
	if s.conversation.store != nil {
		if snapshot, err := s.conversation.store.Snapshot(); err == nil {
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

func (s *interactiveSession) persistSettings(
	ctx context.Context,
	current config.Config,
	changes map[config.Setting]string,
) (config.Config, error) {
	next, err := current.WithSettings(changes)
	if err != nil {
		return config.Config{}, err
	}
	if s.application == nil ||
		s.application.dependencies.saveSettings == nil {
		return config.Config{}, fmt.Errorf("app: configuration persistence is unavailable")
	}
	if err := s.application.dependencies.saveSettings(ctx, current.Paths, changes); err != nil {
		return config.Config{}, fmt.Errorf("app: save settings: %w", err)
	}
	return next, nil
}

const savedDefaultsOverrideNotice = "\nA flag, environment variable, or project setting overrides the saved defaults on the next startup. This Session uses your selection."

func savedOverrideNotice(configuration config.Config, changes map[config.Setting]string) string {
	if !configuration.SavedValuesOverridden(changes) {
		return ""
	}
	return savedDefaultsOverrideNotice
}

func settingSelectionValue(name, input string) (string, error) {
	fields := strings.Fields(input)
	if len(fields) != 1 {
		return "", fmt.Errorf(
			"app: usage: /%s <%s>",
			name,
			name,
		)
	}
	return fields[0], nil
}

func savedSettingMessage(
	name string,
	value string,
	overridden bool,
) string {
	message := fmt.Sprintf(
		"Set %s to %s for the current Session and saved it to global settings.",
		name,
		value,
	)
	if overridden {
		message += savedDefaultsOverrideNotice
	}
	return message
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
