// Package app wires AICE's process-level dependencies and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
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
	return newCommand(dependencies{
		loadConfig:  config.Load,
		saveSetting: config.SaveSetting,
		saveAPIKey:  defaultSaveAPIKey,
		newModel:    modelForConfiguration,
		runTUI:      tui.Run,
	})
}

type dependencies struct {
	loadConfig                 func() (config.Config, error)
	saveSetting                func(config.Setting, string) error
	saveAPIKey                 func(provider, apiKey string) (string, error)
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
		dependencies.saveAPIKey = defaultSaveAPIKey
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
	path, err := a.dependencies.saveAPIKey(request.Provider, request.APIKey)
	if err != nil {
		return "", fmt.Errorf("app: save API key: %w", err)
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

	environment, err := a.newRunEnvironment(ctx, request.Workspace)
	if err != nil {
		return err
	}
	if environment.loop == nil {
		return credentialNotConfiguredError(environment.configuration)
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

	environment, err := a.newRunEnvironment(ctx, request.Workspace)
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
		tools:         environment.tools,
	}
	runErr := a.dependencies.runTUI(ctx, runner, tui.Options{
		Input:            request.Input,
		Output:           request.Output,
		Model:            environment.model,
		Thinking:         environment.options.Thinking,
		APIKeyConfigured: providerConfigured(environment.configuration),
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
	ctx context.Context,
	workingDirectory string,
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
	var loop *agent.Loop
	if providerConfigured(configured.configuration) {
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

func (a *application) loadConfiguredModel() (configuredModel, error) {
	configuration, err := a.dependencies.loadConfig()
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

func (a *application) newConfiguredModel() (configuredModel, error) {
	configured, err := a.loadConfiguredModel()
	if err != nil {
		return configuredModel{}, err
	}
	if !providerConfigured(configured.configuration) {
		return configuredModel{}, credentialNotConfiguredError(
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
	if !providerConfigured(configuration) {
		return nil, credentialNotConfiguredError(configuration)
	}
	service, err := a.dependencies.newModel(configuration)
	if err != nil {
		return nil, fmt.Errorf("app: create model: %w", err)
	}
	loop, err := agent.NewLoop(service, tools)
	if err != nil {
		return nil, fmt.Errorf("app: create agent loop: %w", err)
	}
	return loop, nil
}

func credentialNotConfiguredError(configuration config.Config) error {
	switch configuration.Provider {
	case string(opencode.ProviderID):
		return fmt.Errorf(
			"OpenCode Go API key is not configured; run /login in interactive "+
				"mode or set %s",
			config.EnvOpenCodeAPIKey,
		)
	case string(deepseek.ProviderID):
		return fmt.Errorf(
			"DeepSeek API key is not configured; run /login in interactive mode "+
				"or set %s",
			config.EnvDeepSeekAPIKey,
		)
	default:
		return fmt.Errorf(
			"API key for provider %q is not configured; run /login in "+
				"interactive mode",
			configuration.Provider,
		)
	}
}

// knownProviders returns the provider identifiers AICE supports.
func knownProviders() []string {
	return []string{
		string(deepseek.ProviderID),
		string(opencode.ProviderID),
	}
}

// supportedProvider reports whether provider is one AICE can serve.
func supportedProvider(provider string) bool {
	return provider == string(deepseek.ProviderID) ||
		provider == string(opencode.ProviderID)
}

// providerConfigured reports whether the selected provider has a credential.
func providerConfigured(configuration config.Config) bool {
	switch configuration.Provider {
	case string(opencode.ProviderID):
		return configuration.OpenCodeAPIKey != ""
	case string(deepseek.ProviderID):
		return configuration.DeepSeekAPIKey != ""
	}
	return false
}

// providerLabel returns the display name for a provider identifier.
func providerLabel(provider string) string {
	switch provider {
	case string(opencode.ProviderID):
		return "OpenCode Go"
	case string(deepseek.ProviderID):
		return "DeepSeek"
	}
	return provider
}

// modelForConfiguration constructs the model service for the provider selected
// in the configuration.
func modelForConfiguration(configuration config.Config) (agent.Model, error) {
	switch configuration.Provider {
	case string(opencode.ProviderID):
		return opencode.New(opencode.Config{
			APIKey:  configuration.OpenCodeAPIKey,
			BaseURL: configuration.OpenCodeBaseURL,
		})
	case string(deepseek.ProviderID), "":
		return deepseek.New(deepseek.Config{
			APIKey:  configuration.DeepSeekAPIKey,
			BaseURL: configuration.DeepSeekBaseURL,
		})
	default:
		return nil, fmt.Errorf("app: unsupported provider %q", configuration.Provider)
	}
}

// defaultSaveAPIKey stores a credential in the auth file of the provider it
// belongs to, preserving any other provider credentials already present.
func defaultSaveAPIKey(provider, apiKey string) (string, error) {
	switch provider {
	case string(opencode.ProviderID):
		return config.SaveOpenCodeAPIKey(apiKey)
	case string(deepseek.ProviderID):
		return config.SaveDeepSeekAPIKey(apiKey)
	default:
		return "", fmt.Errorf("app: unsupported provider %q", provider)
	}
}

// modelsForProvider returns the model catalog for a provider. Unknown providers
// fall back to DeepSeek so callers that already validated the provider can rely
// on a non-empty catalog.
func modelsForProvider(provider string) []llm.Model {
	switch provider {
	case string(opencode.ProviderID):
		return opencode.Models()
	default:
		return deepseek.Models()
	}
}

// providerDefaultModel returns the default model for a provider.
func providerDefaultModel(provider string) llm.Model {
	switch provider {
	case string(opencode.ProviderID):
		return opencode.DefaultModel()
	default:
		return deepseek.DefaultModel()
	}
}

// modelForProvider looks a model ID up in one provider's catalog.
func modelForProvider(provider, id string) (llm.Model, bool) {
	for _, model := range modelsForProvider(provider) {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

// modelIDsForProvider returns the model IDs of one provider's catalog.
func modelIDsForProvider(provider string) []string {
	models := modelsForProvider(provider)
	ids := make([]string, len(models))
	for index, model := range models {
		ids[index] = model.ID
	}
	return ids
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
	if !supportedProvider(provider) {
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			strings.Join(knownProviders(), ", "),
		)
	}

	modelID := configuration.Model
	if modelID == "" {
		modelID = providerDefaultModel(provider).ID
	}
	for _, model := range modelsForProvider(provider) {
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
	tools         []agent.Tool
}

func (s *interactiveSession) Run(
	ctx context.Context,
	promptText string,
	sink agent.AgentEventSink,
) error {
	if s.loop == nil {
		return credentialNotConfiguredError(s.configuration)
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
