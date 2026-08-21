// Package app wires AICE's process-level dependencies and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/ch1lam/aice-cli/internal/tui"
	"github.com/ch1lam/aice-cli/internal/update"
)

const (
	defaultSystemPrompt = "You are AICE, a coding agent. Use the available " +
		"coding tools to inspect and modify the working environment when needed. " +
		"Give concise, evidence-based answers and never claim changes you " +
		"did not make."
	defaultCompactionMaxTokens int64 = 16_000
)

// NewCommand assembles the production AICE command tree.
func NewCommand() (*cobra.Command, error) {
	providers := defaultProviders()
	return newCommand(dependencies{
		loadConfig:  config.Load,
		saveSetting: config.SaveSetting,
		saveAPIKey: func(providerID, apiKey string) (string, error) {
			return defaultSaveAPIKey(providers, providerID, apiKey)
		},
		newModel: func(configuration config.Config) (agent.Model, error) {
			return modelForConfiguration(providers, configuration)
		},
		checkUpdate: func(ctx context.Context) (update.StartupResult, error) {
			return update.CheckStartup(ctx, update.Options{Current: cli.Version})
		},
		runTUI:    tui.Run,
		providers: providers,
	})
}

// defaultProviders returns the built-in provider registry in menu order.
func defaultProviders() []provider.Provider {
	return []provider.Provider{
		&deepseek.Provider{},
		&opencode.Provider{},
		&openai.Provider{},
		&custom.Provider{},
	}
}

type dependencies struct {
	loadConfig                 func() (config.Config, error)
	saveSetting                func(config.Setting, string) error
	saveAPIKey                 func(provider, apiKey string) (string, error)
	newModel                   func(config.Config) (agent.Model, error)
	checkUpdate                func(context.Context) (update.StartupResult, error)
	runTUI                     func(context.Context, interaction.Runner, tui.Options) error
	runTrustTUI                func(context.Context, tui.TrustPromptOptions) (trust.Choice, error)
	compactionKeepRecentTokens int64
	providers                  []provider.Provider
}

func newCommand(dependencies dependencies) (*cobra.Command, error) {
	if dependencies.loadConfig == nil {
		return nil, fmt.Errorf("app: config loader is required")
	}
	if dependencies.newModel == nil {
		return nil, fmt.Errorf("app: model factory is required")
	}
	if dependencies.saveAPIKey == nil {
		providers := dependencies.providers
		dependencies.saveAPIKey = func(providerID, apiKey string) (string, error) {
			return defaultSaveAPIKey(providers, providerID, apiKey)
		}
	}
	if dependencies.providers == nil {
		dependencies.providers = defaultProviders()
	}
	if dependencies.runTUI == nil {
		dependencies.runTUI = tui.Run
	}
	if dependencies.runTrustTUI == nil {
		dependencies.runTrustTUI = tui.RunTrustPrompt
	}
	if dependencies.compactionKeepRecentTokens == 0 {
		dependencies.compactionKeepRecentTokens = session.DefaultKeepRecentTokens
	}
	if dependencies.compactionKeepRecentTokens < 0 {
		return nil, fmt.Errorf("app: compaction keep-recent tokens must be positive")
	}

	application := &application{dependencies: dependencies}
	return cli.NewRootCommand(cli.Dependencies{
		Printer:      application,
		Interactor:   application,
		Compactor:    application,
		Navigator:    application,
		Configurator: application,
		Updater:      application,
	})
}

type application struct {
	dependencies dependencies
}

// SaveAPIKey stores one global credential entered through the CLI.
func (a *application) SaveAPIKey(
	ctx context.Context,
	request cli.APIKeyRequest,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("app: context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := a.dependencies.saveAPIKey(request.Provider, request.APIKey)
	if err != nil {
		return "", fmt.Errorf("app: save API key: %w", err)
	}
	return path, nil
}

// Update runs one aice update invocation against the GitHub release the
// running binary was distributed through.
func (a *application) Update(
	ctx context.Context,
	request cli.UpdateRequest,
	output io.Writer,
) error {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	opts := update.Options{Current: cli.Version}
	if request.Check {
		result, err := update.Check(ctx, opts)
		if err != nil {
			return err
		}
		return printUpdateCheck(output, result)
	}
	result, err := update.Update(ctx, opts, request.Force)
	if err != nil {
		return err
	}
	return printUpdateResult(output, result)
}

func printUpdateCheck(output io.Writer, result update.CheckResult) error {
	if result.Available {
		_, err := fmt.Fprintf(
			output,
			"update available: %s -> %s (run `aice update`)\n",
			result.Current,
			result.Latest,
		)
		return err
	}
	if result.Current == result.Latest {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Latest)
		return err
	}
	// The current version cannot be compared (for example a dev build), so the
	// latest release is reported without claiming the install is current.
	_, err := fmt.Fprintf(output, "latest release is %s\n", result.Latest)
	return err
}

func printUpdateResult(output io.Writer, result update.UpdateResult) error {
	if !result.Updated {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Latest)
		return err
	}
	if _, err := fmt.Fprintf(
		output,
		"updated aice %s -> %s\n",
		result.Current,
		result.Latest,
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "restart aice to use the new version\n")
	return err
}

func (a *application) Print(
	ctx context.Context,
	request cli.PrintRequest,
	output io.Writer,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	environment, err := a.newRunEnvironment(
		ctx,
		request.Workspace,
		request.ProjectTrustOverride,
		nil,
	)
	if err != nil {
		return err
	}
	if environment.loop == nil {
		return credentialNotConfiguredError(
			a.dependencies.providers,
			environment.configuration,
		)
	}
	store, history, _, err := prepareSession(
		ctx,
		environment.workspace,
		request.Session,
		false,
	)
	if err != nil {
		return err
	}
	if store != nil {
		defer func() {
			returnErr = errors.Join(returnErr, store.Close())
		}()
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent(request.Prompt).Part())
	if err != nil {
		return fmt.Errorf("app: create prompt: %w", err)
	}

	printer := &streamPrinter{output: output}
	result, loopErr := environment.loop.Run(ctx, agent.RunInput{
		Model:        environment.model,
		SystemPrompt: environment.systemPrompt,
		History:      history,
		Prompt:       prompt,
		Options:      environment.options,
		Compactor:    a.sessionCompactor(store),
	}, printer.Accept)
	finishErr := printer.Finish()
	var persistErr error
	if store != nil {
		persistErr = appendSessionTurn(ctx, store, result.Messages())
	}
	if loopErr != nil {
		return errors.Join(
			fmt.Errorf("app: run agent: %w", loopErr),
			finishErr,
			persistErr,
		)
	}
	return errors.Join(finishErr, persistErr)
}

// Interactive runs one multi-turn terminal session.
func (a *application) Interactive(
	ctx context.Context,
	request cli.InteractiveRequest,
) error {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if request.Input == nil {
		return fmt.Errorf("app: input is required")
	}
	if request.Output == nil {
		return fmt.Errorf("app: output is required")
	}
	askUI := func(cwd string) (trust.Choice, error) {
		return a.dependencies.runTrustTUI(ctx, tui.TrustPromptOptions{
			Input:   request.Input,
			Output:  request.Output,
			CWD:     cwd,
			Choices: trust.Choices(cwd),
		})
	}
	environment, err := a.newRunEnvironment(
		ctx,
		request.Workspace,
		request.ProjectTrustOverride,
		askUI,
	)
	if err != nil {
		return err
	}
	store, history, usage, err := prepareSession(
		ctx,
		environment.workspace,
		request.Session,
		true,
	)
	if err != nil {
		return err
	}

	runner := &interactiveSession{
		application:   a,
		loop:          environment.loop,
		guard:         environment.guard,
		guardAdapter:  environment.guardAdapter,
		guardRequests: make(chan interaction.GuardRequest, 4),
		store:         store,
		history:       history,
		model:         environment.model,
		options:       environment.options,
		configuration: environment.configuration,
		tools:         environment.tools,
		systemPrompt:  environment.systemPrompt,
		trustStore:    trust.NewStore(environment.configuration.Paths.GlobalTrust),
		workspace:     environment.workspace,
		workspacePath: environment.workspace.PhysicalPath(),
		trustDecision: environment.trust.Decision,
		trustSource:   environment.trust.Source,
		providers:     a.dependencies.providers,
		totalUsage:    usage,
	}
	if runner.loop != nil {
		runner.loop.SetGuardAskHandler(runner.handleGuardAsk)
	}
	var checkUpdate tui.UpdateChecker
	if a.dependencies.checkUpdate != nil {
		checkUpdate = a.checkForUpdate
	}
	runErr := a.dependencies.runTUI(ctx, runner, tui.Options{
		Input:       request.Input,
		Output:      request.Output,
		Model:       interaction.DisplayModel{ID: environment.model.ID},
		Thinking:    interaction.DisplayThinking(environment.options.Thinking),
		CheckUpdate: checkUpdate,
		APIKeyConfigured: providerConfigured(
			a.dependencies.providers,
			environment.configuration,
		),
		Usage:            newDisplayUsage(usage),
		WorkingDirectory: environment.workspace.Path(),
		Version:          cli.Version,
	})
	closeErr := store.Close()
	if runErr != nil {
		return errors.Join(fmt.Errorf("app: run TUI: %w", runErr), closeErr)
	}
	return closeErr
}

func (a *application) checkForUpdate(
	ctx context.Context,
) (tui.UpdateCheckResult, error) {
	result, err := a.dependencies.checkUpdate(ctx)
	if err != nil {
		return tui.UpdateCheckResult{}, fmt.Errorf("app: check for updates: %w", err)
	}

	status := tui.UpdateCheckStatusUnknown
	switch result.Status {
	case update.StartupStatusDisabled:
		status = tui.UpdateCheckStatusDisabled
	case update.StartupStatusDevelopment:
		status = tui.UpdateCheckStatusDevelopment
	case update.StartupStatusCurrent:
		status = tui.UpdateCheckStatusCurrent
	case update.StartupStatusAvailable:
		status = tui.UpdateCheckStatusAvailable
	default:
		return tui.UpdateCheckResult{}, fmt.Errorf(
			"app: unsupported startup update status %d",
			result.Status,
		)
	}
	return tui.UpdateCheckResult{
		Status: status,
		Latest: result.Latest,
	}, nil
}

type runEnvironment struct {
	loop          *agent.Loop
	workspace     *tool.Workspace
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
	tools         []agent.Tool
	systemPrompt  string
	trust         trust.Resolution
	guard         *guard.Guard
	guardAdapter  *guardAdapter
}

type configuredModel struct {
	service       agent.Model
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
}

func (a *application) newRunEnvironment(
	ctx context.Context,
	workingDirectory string,
	override *bool,
	askUI trust.AskFunc,
) (*runEnvironment, error) {
	workspace, err := tool.NewWorkspace(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("app: create workspace: %w", err)
	}
	configured, err := a.loadConfiguredModel()
	if err != nil {
		return nil, err
	}
	// Make the external helpers the tools need (ripgrep, Git Bash on Windows)
	// available before constructing them; a failure is logged, not fatal, and
	// the affected tools degrade to unavailable stubs.
	if paths, err := config.DefaultPaths(); err == nil {
		if err := deps.Ensure(ctx, deps.DefaultOptions().WithBinDir(paths.BinDir)); err != nil {
			fmt.Fprintf(os.Stderr, "aice: warning: %v\n", err)
		}
	}
	tools, err := newBuiltInTools(workspace)
	if err != nil {
		return nil, err
	}
	project, err := a.resolveProjectContext(
		ctx,
		workspace,
		configured.configuration,
		override,
		askUI,
		tools,
	)
	if err != nil {
		return nil, err
	}
	var loop *agent.Loop
	var g *guard.Guard
	var adapter *guardAdapter
	if providerConfigured(a.dependencies.providers, configured.configuration) {
		loop, g, adapter, err = a.newAgentLoopWithWorkspace(configured.configuration, tools, workspace.PhysicalPath())
		if err != nil {
			return nil, err
		}
	}
	return &runEnvironment{
		loop:          loop,
		workspace:     workspace,
		configuration: configured.configuration,
		model:         configured.model,
		options:       configured.options,
		tools:         tools,
		systemPrompt:  project.systemPrompt,
		trust:         project.trust,
		guard:         g,
		guardAdapter:  adapter,
	}, nil
}

func (a *application) loadConfiguredModel() (configuredModel, error) {
	configuration, err := a.dependencies.loadConfig()
	if err != nil {
		return configuredModel{}, fmt.Errorf(
			"app: load configuration: %w",
			err,
		)
	}
	selectedModel, options, err := resolveModelSettings(
		a.dependencies.providers,
		configuration,
	)
	if err != nil {
		return configuredModel{}, err
	}
	configuration.Provider = string(selectedModel.Provider)
	configuration.Model = selectedModel.ID
	return configuredModel{
		configuration: configuration,
		model:         selectedModel,
		options:       options,
	}, nil
}

func (a *application) newConfiguredModel() (configuredModel, error) {
	configured, err := a.loadConfiguredModel()
	if err != nil {
		return configuredModel{}, err
	}
	if !providerConfigured(a.dependencies.providers, configured.configuration) {
		return configuredModel{}, credentialNotConfiguredError(
			a.dependencies.providers,
			configured.configuration,
		)
	}
	service, err := a.dependencies.newModel(configured.configuration)
	if err != nil {
		return configuredModel{}, fmt.Errorf("app: create model: %w", err)
	}
	configured.service = service
	return configured, nil
}

func (a *application) newAgentLoop(
	configuration config.Config,
	tools []agent.Tool,
) (*agent.Loop, error) {
	loop, _, _, err := a.newAgentLoopWithWorkspace(configuration, tools, "")
	return loop, err
}

func (a *application) newAgentLoopWithWorkspace(
	configuration config.Config,
	tools []agent.Tool,
	workspace string,
) (*agent.Loop, *guard.Guard, *guardAdapter, error) {
	if !providerConfigured(a.dependencies.providers, configuration) {
		return nil, nil, nil, credentialNotConfiguredError(
			a.dependencies.providers,
			configuration,
		)
	}
	service, err := a.dependencies.newModel(configuration)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("app: create model: %w", err)
	}
	// Built-in guard: intrinsic execution gate, not a plugin. Workspace-scoped
	// so .env relative to the project is correctly recognized. Disabled only
	// when explicitly configured off (future: guard config in settings.json).
	g, err := guard.New(workspace, guard.Config{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("app: create guard: %w", err)
	}
	adapter := &guardAdapter{inner: g}
	loop, err := agent.NewLoop(service, tools, agent.WithGuard(adapter))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("app: create agent loop: %w", err)
	}
	return loop, g, adapter, nil
}

// guardAdapter bridges internal/guard.Guard to agent.Guard without making
// agent import guard directly in its core package. It lives in app which
// already depends on both, preserving the "consumer defines interface" rule.
type guardAdapter struct {
	inner *guard.Guard
}

func (g *guardAdapter) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	if g == nil || g.inner == nil {
		return agent.GuardResult{Decision: agent.GuardAllow}, nil
	}
	res, err := g.inner.Check(ctx, call)
	if err != nil {
		return agent.GuardResult{}, err
	}
	switch res.Decision {
	case guard.DecisionDeny:
		return agent.GuardResult{Decision: agent.GuardDeny, Reason: res.Reason, RuleID: res.RuleID, Action: agent.GuardAction{Kind: string(res.Action.Kind), Path: res.Action.Path, Command: res.Action.Command, ToolName: res.Action.ToolName}}, nil
	case guard.DecisionAsk:
		return agent.GuardResult{Decision: agent.GuardAsk, Reason: res.Reason, RuleID: res.RuleID, Action: agent.GuardAction{Kind: string(res.Action.Kind), Path: res.Action.Path, Command: res.Action.Command, ToolName: res.Action.ToolName}}, nil
	default:
		return agent.GuardResult{Decision: agent.GuardAllow}, nil
	}
}

func (g *guardAdapter) Inner() *guard.Guard { return g.inner }

// findProvider returns the registered provider matching an identifier, or nil.
func findProvider(providers []provider.Provider, providerID string) provider.Provider {
	for _, candidate := range providers {
		if string(candidate.ProviderID()) == providerID {
			return candidate
		}
	}
	return nil
}

func credentialNotConfiguredError(
	providers []provider.Provider,
	configuration config.Config,
) error {
	if candidate := findProvider(providers, configuration.Provider); candidate != nil {
		return candidate.CredentialNotConfiguredError()
	}
	return fmt.Errorf(
		"API key for provider %q is not configured; run /login in "+
			"interactive mode",
		configuration.Provider,
	)
}

// knownProviders returns the provider identifiers AICE supports.
func knownProviders(providers []provider.Provider) []string {
	ids := make([]string, 0, len(providers))
	for _, candidate := range providers {
		ids = append(ids, string(candidate.ProviderID()))
	}
	return ids
}

// supportedProvider reports whether provider is one AICE can serve.
func supportedProvider(providers []provider.Provider, providerID string) bool {
	return findProvider(providers, providerID) != nil
}

// providerConfigured reports whether the selected provider has a credential.
func providerConfigured(
	providers []provider.Provider,
	configuration config.Config,
) bool {
	candidate := findProvider(providers, configuration.Provider)
	return candidate != nil && candidate.Configured(configuration)
}

// providerLabel returns the display name for a provider identifier.
func providerLabel(providers []provider.Provider, providerID string) string {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.Label()
	}
	return providerID
}

// modelForConfiguration constructs the model service for the provider selected
// in the configuration.
func modelForConfiguration(
	providers []provider.Provider,
	configuration config.Config,
) (agent.Model, error) {
	providerID := configuration.Provider
	if providerID == "" {
		providerID = string(deepseek.ProviderID)
	}
	candidate := findProvider(providers, providerID)
	if candidate == nil {
		return nil, fmt.Errorf("app: unsupported provider %q", configuration.Provider)
	}
	return candidate.New(configuration)
}

// defaultSaveAPIKey stores a credential in the auth file of the provider it
// belongs to, preserving any other provider credentials already present.
func defaultSaveAPIKey(
	providers []provider.Provider,
	providerID, apiKey string,
) (string, error) {
	candidate := findProvider(providers, providerID)
	if candidate == nil {
		return "", fmt.Errorf("app: unsupported provider %q", providerID)
	}
	return candidate.SaveAPIKey(apiKey)
}

// modelsForProvider returns the model catalog for a provider. Unknown providers
// fall back to DeepSeek so callers that already validated the provider can rely
// on a non-empty catalog.
func modelsForProvider(providers []provider.Provider, providerID string) []llm.Model {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.Models()
	}
	if deepSeek := findProvider(providers, string(deepseek.ProviderID)); deepSeek != nil {
		return deepSeek.Models()
	}
	return nil
}

// providerDefaultModel returns the default model for a provider.
func providerDefaultModel(providers []provider.Provider, providerID string) llm.Model {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.DefaultModel()
	}
	if deepSeek := findProvider(providers, string(deepseek.ProviderID)); deepSeek != nil {
		return deepSeek.DefaultModel()
	}
	return llm.Model{}
}

// modelForProvider looks a model ID up in one provider's catalog.
func modelForProvider(
	providers []provider.Provider,
	providerID, id string,
) (llm.Model, bool) {
	for _, model := range modelsForProvider(providers, providerID) {
		if model.ID == id {
			return model, true
		}
	}
	if providerID == string(custom.ProviderID) && strings.TrimSpace(id) != "" {
		return custom.ModelForID(id), true
	}
	return llm.Model{}, false
}

// modelIDsForProvider returns the model IDs of one provider's catalog.
func modelIDsForProvider(providers []provider.Provider, providerID string) []string {
	models := modelsForProvider(providers, providerID)
	ids := make([]string, len(models))
	for index, model := range models {
		ids[index] = model.ID
	}
	return ids
}

func resolveModelSettings(
	providers []provider.Provider,
	configuration config.Config,
) (llm.Model, llm.StreamOptions, error) {
	switch configuration.Thinking {
	case llm.ThinkingLevelUnknown,
		llm.ThinkingLevelOff,
		llm.ThinkingLevelMinimal,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax:
	default:
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported thinking level %q",
			configuration.Thinking,
		)
	}

	providerID := configuration.Provider
	if providerID == "" {
		providerID = string(deepseek.ProviderID)
	}
	if !supportedProvider(providers, providerID) {
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			providerID,
			strings.Join(knownProviders(providers), ", "),
		)
	}

	modelID := configuration.Model
	if modelID == "" {
		modelID = providerDefaultModel(providers, providerID).ID
	}
	// The custom provider is the Pi-inspired catch-all: any model ID is
	// materialized on the fly with safe defaults (only `id` required).
	if providerID == string(custom.ProviderID) {
		model := custom.ModelForID(modelID)
		requested := configuration.Thinking
		if requested == llm.ThinkingLevelUnknown {
			requested = llm.DefaultThinkingLevel
		}
		return model, llm.StreamOptions{
			Thinking: llm.ClampThinkingLevel(model, requested),
		}, nil
	}
	for _, model := range modelsForProvider(providers, providerID) {
		if model.ID != modelID {
			continue
		}
		requested := configuration.Thinking
		if requested == llm.ThinkingLevelUnknown {
			requested = llm.DefaultThinkingLevel
		}
		return model, llm.StreamOptions{
			Thinking: llm.ClampThinkingLevel(model, requested),
		}, nil
	}
	return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
		"app: unsupported model %q for provider %q",
		modelID,
		providerID,
	)
}

type interactiveSession struct {
	application    *application
	stateMu        sync.RWMutex
	loop           *agent.Loop
	store          *session.Store
	historySyncMu  sync.Mutex
	historyMu      sync.RWMutex
	history        []llm.AgentMessage
	activeMainRun  *mainRunState
	model          llm.Model
	options        llm.StreamOptions
	configuration  config.Config
	tools          []agent.Tool
	systemPrompt   string
	trustStore     *trust.Store
	workspace      *tool.Workspace
	workspacePath  string
	trustDecision  trust.Decision
	trustSource    trust.Source
	providers      []provider.Provider
	totalUsage     llm.Usage
	sessionChanged bool
	guard          *guard.Guard
	guardAdapter   *guardAdapter
	guardRequests  chan interaction.GuardRequest

	// sideMu guards the ephemeral /btw thread registry below. sideClock is
	// the injectable time source used for idle windows and expiry; nil means
	// wall-clock time.
	sideMu      sync.Mutex
	sideThreads map[uint64]*sideThread
	sideNextID  uint64
	sideRunning int
	sideClock   func() time.Time
}

type interactiveSettings struct {
	loop          *agent.Loop
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
	systemPrompt  string
}

func (s *interactiveSession) settingsSnapshot() interactiveSettings {
	if s == nil {
		return interactiveSettings{}
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return interactiveSettings{
		loop:          s.loop,
		configuration: s.configuration,
		model:         cloneModel(s.model),
		options:       cloneStreamOptions(s.options),
		systemPrompt:  s.systemPrompt,
	}
}

// GuardRequests exposes pending guard confirmations for the TUI.
func (s *interactiveSession) GuardRequests() <-chan interaction.GuardRequest {
	if s == nil {
		return nil
	}
	return s.guardRequests
}

func (s *interactiveSession) handleGuardAsk(ctx context.Context, call llm.ToolCall, result agent.GuardResult) (agent.GuardDecision, error) {
	if s == nil || s.guardRequests == nil {
		return agent.GuardDeny, nil
	}
	// Use a small ID for display; call.ID is the tool-call ID.
	reqID := call.ID
	if reqID == "" {
		reqID = result.RuleID
	}
	reply := make(chan interaction.GuardDecision, 1)
	req := interaction.GuardRequest{
		ID:       reqID,
		ToolName: call.Name,
		Reason:   result.Reason,
		RuleID:   result.RuleID,
		Command:  result.Action.Command,
		Path:     result.Action.Path,
		Reply:    reply,
	}
	select {
	case <-ctx.Done():
		return agent.GuardDeny, ctx.Err()
	case s.guardRequests <- req:
	}
	select {
	case <-ctx.Done():
		return agent.GuardDeny, ctx.Err()
	case decision := <-reply:
		switch decision {
		case interaction.GuardDecisionAllowAlways:
			if s.guard != nil {
				switch {
				case result.RuleID == "pathAccess.ask":
					abs := s.guard.ResolveAbsolute(result.Action.Path, result.Action.ToolName)
					s.guard.AllowPathSession(abs, false)
					// Also allow parent directory so sibling files under same granted file are reachable
					// when the user explicitly chooses "always" for an outside path.
				case result.RuleID == "permissionGate.dangerous":
					if result.Action.Command != "" {
						s.guard.AllowCommandSession(result.Action.Command)
					} else if result.Action.Path != "" {
						s.guard.AllowPathSession(result.Action.Path, false)
					}
				default:
					if result.Action.Path != "" {
						s.guard.AllowSession(result.Action.Path)
					}
				}
			}
			return agent.GuardAllow, nil
		case interaction.GuardDecisionAllowOnce:
			return agent.GuardAllow, nil
		default:
			return agent.GuardDeny, nil
		}
	}
}

func newBuiltInTools(workspace *tool.Workspace) ([]agent.Tool, error) {
	if workspace == nil {
		return nil, fmt.Errorf("app: workspace is required")
	}
	// A tool whose external executable is missing becomes an unavailable stub
	// instead of failing the whole tool set, so the app always starts and the
	// agent can explain the gap to the user.
	tools := make([]agent.Tool, 0, 7)
	add := func(name string, current agent.Tool, err error) {
		if err != nil {
			current = tool.NewUnavailable(name, err)
		}
		tools = append(tools, current)
	}

	read, err := tool.NewRead(workspace)
	add("read", read, err)
	write, err := tool.NewWrite(workspace)
	add("write", write, err)
	edit, err := tool.NewEdit(workspace)
	add("edit", edit, err)
	bash, err := tool.NewBash(workspace)
	add("bash", bash, err)
	grep, err := tool.NewGrep(workspace)
	add("grep", grep, err)
	find, err := tool.NewFind(workspace)
	add("find", find, err)
	ls, err := tool.NewLS(workspace)
	add("ls", ls, err)
	return tools, nil
}

type streamPrinter struct {
	output          io.Writer
	pendingText     bool
	lastWrittenByte byte
}

func (p *streamPrinter) Accept(ctx context.Context, event agent.AgentEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Type == agent.EventTypeMessageEnd {
		if _, ok := event.Message.(llm.AssistantMessage); ok {
			return p.finishLine()
		}
	}
	if event.Type != agent.EventTypeMessageUpdate || event.AssistantMessageEvent == nil {
		return nil
	}
	if event.AssistantMessageEvent.Type != llm.EventTypeTextDelta ||
		event.AssistantMessageEvent.Delta == "" {
		return nil
	}

	delta := event.AssistantMessageEvent.Delta
	if _, err := io.WriteString(p.output, delta); err != nil {
		return fmt.Errorf("app: write streamed response: %w", err)
	}
	p.pendingText = true
	p.lastWrittenByte = delta[len(delta)-1]
	return nil
}

func (p *streamPrinter) Finish() error {
	return p.finishLine()
}

func (p *streamPrinter) finishLine() error {
	if !p.pendingText {
		return nil
	}
	p.pendingText = false
	if p.lastWrittenByte == '\n' {
		return nil
	}
	if _, err := io.WriteString(p.output, "\n"); err != nil {
		return fmt.Errorf("app: finish streamed response: %w", err)
	}
	p.lastWrittenByte = '\n'
	return nil
}
