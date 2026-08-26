// Package app wires AICE's process-level dependencies and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/ch1lam/aice-cli/internal/tui"
	"github.com/ch1lam/aice-cli/internal/update"
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
		newModel: func(configuration config.Config) (llm.Streamer, error) {
			return modelForConfiguration(providers, configuration)
		},
		checkUpdate: func(ctx context.Context) (update.StartupResult, error) {
			return update.CheckStartup(ctx, update.Options{Current: cli.Version})
		},
		runTUI:    tui.Run,
		providers: providers,
	})
}

type dependencies struct {
	loadConfig                 func() (config.Config, error)
	saveSetting                func(config.Setting, string) error
	saveAPIKey                 func(provider, apiKey string) (string, error)
	newModel                   func(config.Config) (llm.Streamer, error)
	checkUpdate                func(context.Context) (update.StartupResult, error)
	runTUI                     func(context.Context, interaction.Runner, tui.Options) error
	runTrustTUI                func(context.Context, tui.TrustPromptOptions) (trust.Choice, error)
	compactionKeepRecentTokens int64
	providers                  []provider.Provider
	userHomeDir                func() (string, error)
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
	if !providerConfigured(a.dependencies.providers, environment.configuration) {
		return credentialNotConfiguredError(
			a.dependencies.providers,
			environment.configuration,
		)
	}
	loop, err := a.newAgentLoopWithOptions(
		environment.configuration,
		environment.tools,
		agent.WithGuard(environment.guardAdapter),
	)
	if err != nil {
		return err
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
	result, loopErr := loop.Run(ctx, agent.RunInput{
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
		skills:        environment.skills,
		skillDiags:    environment.skillDiags,
		trustStore:    trust.NewStore(environment.configuration.Paths.GlobalTrust),
		workspace:     environment.workspace,
		workspacePath: environment.workspace.PhysicalPath(),
		trustDecision: environment.trust.Decision,
		trustSource:   environment.trust.Source,
		providers:     a.dependencies.providers,
		totalUsage:    usage,
	}
	if providerConfigured(a.dependencies.providers, environment.configuration) {
		loop, err := a.newAgentLoopWithOptions(
			environment.configuration,
			environment.tools,
			agent.WithGuard(environment.guardAdapter),
			agent.WithGuardAskHandler(runner.handleGuardAsk),
		)
		if err != nil {
			return errors.Join(err, store.Close())
		}
		runner.loop = loop
	}
	var checkUpdate tui.UpdateChecker
	if a.dependencies.checkUpdate != nil {
		checkUpdate = a.checkForUpdate
	}
	thinking, err := displayThinking(environment.options.Thinking)
	if err != nil {
		return errors.Join(err, store.Close())
	}
	runErr := a.dependencies.runTUI(ctx, runner, tui.Options{
		Input:       request.Input,
		Output:      request.Output,
		Model:       interaction.DisplayModel{ID: environment.model.ID},
		Thinking:    thinking,
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
	workspace     *tool.Workspace
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
	tools         []agent.Tool
	systemPrompt  string
	skills        skill.Catalog
	skillDiags    []skill.Diagnostic
	trust         trust.Resolution
	guard         *guard.Guard
	guardAdapter  *guardAdapter
}

type configuredModel struct {
	service       llm.Streamer
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
	resolution, err := a.resolveProjectTrust(
		ctx,
		workspace,
		configured.configuration,
		override,
		askUI,
	)
	if err != nil {
		return nil, err
	}
	discovery := a.discoverRunSkills(
		workspace.PhysicalPath(),
		resolution.Decision == trust.DecisionTrusted,
	)
	tools, err := newBuiltInTools(workspace)
	if err != nil {
		return nil, err
	}
	tools = appendSkillTool(tools, discovery.catalog)
	systemPrompt, err := assembleSystemPrompt(
		workspace,
		configured.configuration,
		resolution.Decision,
		tools,
		discovery.catalog,
	)
	if err != nil {
		return nil, err
	}
	g, adapter, err := newExecutionGuard(
		workspace.PhysicalPath(),
		skillReadOnlyRoots(discovery.catalog),
	)
	if err != nil {
		return nil, err
	}
	return &runEnvironment{
		workspace:     workspace,
		configuration: configured.configuration,
		model:         configured.model,
		options:       configured.options,
		tools:         tools,
		systemPrompt:  systemPrompt,
		skills:        discovery.catalog,
		skillDiags:    discovery.diags,
		trust:         resolution,
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
	_, adapter, err := newExecutionGuard("", nil)
	if err != nil {
		return nil, err
	}
	return a.newAgentLoopWithOptions(
		configuration,
		tools,
		agent.WithGuard(adapter),
	)
}

func (a *application) newAgentLoopWithOptions(
	configuration config.Config,
	tools []agent.Tool,
	options ...agent.LoopOption,
) (*agent.Loop, error) {
	if !providerConfigured(a.dependencies.providers, configuration) {
		return nil, credentialNotConfiguredError(
			a.dependencies.providers,
			configuration,
		)
	}
	service, err := a.dependencies.newModel(configuration)
	if err != nil {
		return nil, fmt.Errorf("app: create model: %w", err)
	}
	loop, err := agent.NewLoop(service, tools, options...)
	if err != nil {
		return nil, fmt.Errorf("app: create agent loop: %w", err)
	}
	return loop, nil
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
	skills         skill.Catalog
	skillDiags     []skill.Diagnostic
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

// rebuildAgentLoop preserves the Session's workspace-scoped guard and its
// memory-only grants when provider credentials replace the model service.
// Inject the Ask handler before publishing the replacement loop.
func (s *interactiveSession) rebuildAgentLoop(
	configuration config.Config,
) (*agent.Loop, error) {
	if s == nil || s.application == nil {
		return nil, fmt.Errorf("app: application is required")
	}
	return s.application.newAgentLoopWithOptions(
		configuration,
		s.tools,
		agent.WithGuard(s.guardAdapter),
		agent.WithGuardAskHandler(s.handleGuardAsk),
	)
}
