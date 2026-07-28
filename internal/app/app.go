// Package app wires AICE's process-level dependencies and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
)

const (
	defaultSystemPrompt = "You are AICE, a coding agent. Use the available " +
		"coding tools to inspect and modify the working environment when needed. " +
		"Give concise, evidence-based answers and never claim changes you " +
		"did not make."
	defaultMaxTurns                  = 12
	defaultMaxToolSteps              = 32
	defaultCompactionMaxTokens int64 = 16_000
)

// NewCommand assembles the production AICE command tree.
func NewCommand() (*cobra.Command, error) {
	return newCommand(dependencies{
		loadConfig:  config.Load,
		saveSetting: config.SaveSetting,
		saveAPIKey:  config.SaveDeepSeekAPIKey,
		newModel: func(config config.Config) (agent.Model, error) {
			return deepseek.New(deepseek.Config{
				APIKey:  config.DeepSeekAPIKey,
				BaseURL: config.DeepSeekBaseURL,
			})
		},
		runTUI: tui.Run,
	})
}

type dependencies struct {
	loadConfig                 func(string) (config.Config, error)
	saveSetting                func(string, config.Scope, config.Setting, string) error
	saveAPIKey                 func(string, string) (string, error)
	newModel                   func(config.Config) (agent.Model, error)
	runTUI                     func(context.Context, tui.Runner, tui.Options) error
	compactionKeepRecentTokens int64
}

func newCommand(dependencies dependencies) (*cobra.Command, error) {
	if dependencies.loadConfig == nil {
		return nil, fmt.Errorf("app: config loader is required")
	}
	if dependencies.newModel == nil {
		return nil, fmt.Errorf("app: model factory is required")
	}
	if dependencies.saveAPIKey == nil {
		dependencies.saveAPIKey = config.SaveDeepSeekAPIKey
	}
	if dependencies.runTUI == nil {
		dependencies.runTUI = tui.Run
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
	path, err := a.dependencies.saveAPIKey(".", request.APIKey)
	if err != nil {
		return "", fmt.Errorf("app: save DeepSeek API key: %w", err)
	}
	return path, nil
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

	environment, err := a.newRunEnvironment(request.Workspace)
	if err != nil {
		return err
	}
	if environment.loop == nil {
		return credentialNotConfiguredError()
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
		SystemPrompt: defaultSystemPrompt,
		History:      history,
		Prompt:       prompt,
		Options:      environment.options,
	}, printer.Accept)
	finishErr := printer.Finish()
	var persistErr error
	if store != nil {
		persistErr = appendSessionRun(ctx, store, result.Messages())
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

	environment, err := a.newRunEnvironment(request.Workspace)
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
		store:         store,
		history:       history,
		model:         environment.model,
		options:       environment.options,
		configuration: environment.configuration,
		workspace:     environment.workspace.Path(),
		tools:         environment.tools,
	}
	runErr := a.dependencies.runTUI(ctx, runner, tui.Options{
		Input:            request.Input,
		Output:           request.Output,
		Model:            environment.model,
		Thinking:         environment.options.Thinking,
		APIKeyConfigured: environment.configuration.DeepSeekAPIKey != "",
		Usage:            usage,
		WorkingDirectory: environment.workspace.Path(),
	})
	closeErr := store.Close()
	if runErr != nil {
		return errors.Join(fmt.Errorf("app: run TUI: %w", runErr), closeErr)
	}
	return closeErr
}

type runEnvironment struct {
	loop          *agent.Loop
	workspace     *tool.Workspace
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
	tools         []agent.Tool
}

type configuredModel struct {
	service       agent.Model
	configuration config.Config
	model         llm.Model
	options       llm.StreamOptions
}

func (a *application) newRunEnvironment(
	workingDirectory string,
) (*runEnvironment, error) {
	workspace, err := tool.NewWorkspace(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("app: create workspace: %w", err)
	}
	configured, err := a.loadConfiguredModel(workspace.Path())
	if err != nil {
		return nil, err
	}
	tools, err := newBuiltInTools(workspace)
	if err != nil {
		return nil, err
	}
	var loop *agent.Loop
	if configured.configuration.DeepSeekAPIKey != "" {
		loop, err = a.newAgentLoop(configured.configuration, tools)
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
	}, nil
}

func (a *application) loadConfiguredModel(
	workspace string,
) (configuredModel, error) {
	configuration, err := a.dependencies.loadConfig(workspace)
	if err != nil {
		return configuredModel{}, fmt.Errorf(
			"app: load configuration: %w",
			err,
		)
	}
	selectedModel, options, err := resolveModelSettings(configuration)
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

func (a *application) newConfiguredModel(
	workspace string,
) (configuredModel, error) {
	configured, err := a.loadConfiguredModel(workspace)
	if err != nil {
		return configuredModel{}, err
	}
	if configured.configuration.DeepSeekAPIKey == "" {
		return configuredModel{}, credentialNotConfiguredError()
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
	if configuration.DeepSeekAPIKey == "" {
		return nil, credentialNotConfiguredError()
	}
	service, err := a.dependencies.newModel(configuration)
	if err != nil {
		return nil, fmt.Errorf("app: create model: %w", err)
	}
	loop, err := agent.NewLoop(service, tools, agent.Limits{
		MaxTurns:     defaultMaxTurns,
		MaxToolSteps: defaultMaxToolSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("app: create agent loop: %w", err)
	}
	return loop, nil
}

func credentialNotConfiguredError() error {
	return fmt.Errorf(
		"DeepSeek API key is not configured; run /login in interactive mode "+
			"or set %s",
		config.EnvDeepSeekAPIKey,
	)
}

func resolveModelSettings(
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

	provider := configuration.Provider
	if provider == "" {
		provider = string(deepseek.ProviderID)
	}
	if provider != string(deepseek.ProviderID) {
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported provider %q",
			provider,
		)
	}

	modelID := configuration.Model
	if modelID == "" {
		modelID = deepseek.DefaultModel().ID
	}
	for _, model := range deepseek.Models() {
		if model.ID != modelID {
			continue
		}
		if !model.SupportsThinking &&
			configuration.Thinking != llm.ThinkingLevelUnknown &&
			configuration.Thinking != llm.ThinkingLevelOff {
			return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
				"app: model %q does not support thinking",
				model.ID,
			)
		}
		return model, llm.StreamOptions{
			Thinking: configuration.Thinking,
		}, nil
	}
	return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
		"app: unsupported model %q for provider %q",
		modelID,
		provider,
	)
}

type interactiveSession struct {
	application   *application
	loop          *agent.Loop
	store         *session.Store
	history       []llm.AgentMessage
	model         llm.Model
	options       llm.StreamOptions
	configuration config.Config
	workspace     string
	tools         []agent.Tool
}

func (s *interactiveSession) Run(
	ctx context.Context,
	promptText string,
	sink agent.AgentEventSink,
) error {
	if s.loop == nil {
		return credentialNotConfiguredError()
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent(promptText).Part())
	if err != nil {
		return fmt.Errorf("app: create prompt: %w", err)
	}
	result, runErr := s.loop.Run(ctx, agent.RunInput{
		Model:        s.model,
		SystemPrompt: defaultSystemPrompt,
		History:      s.history,
		Prompt:       prompt,
		Options:      s.options,
	}, sink)
	messages := result.Messages()
	persistErr := appendSessionRun(ctx, s.store, messages)
	if persistErr == nil {
		s.history = append(s.history, messages...)
	}
	if runErr != nil {
		return errors.Join(
			fmt.Errorf("app: run agent: %w", runErr),
			persistErr,
		)
	}
	return persistErr
}

func newBuiltInTools(workspace *tool.Workspace) ([]agent.Tool, error) {
	read, err := tool.NewRead(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create read tool: %w", err)
	}
	write, err := tool.NewWrite(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create write tool: %w", err)
	}
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create edit tool: %w", err)
	}
	bash, err := tool.NewBash(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create bash tool: %w", err)
	}
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create grep tool: %w", err)
	}
	find, err := tool.NewFind(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create find tool: %w", err)
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create ls tool: %w", err)
	}
	return []agent.Tool{read, write, edit, bash, grep, find, ls}, nil
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
