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
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
)

const (
	defaultSystemPrompt = "You are AICE, a coding agent. Use the available " +
		"read-only workspace tools when repository context is needed. Give concise, " +
		"evidence-based answers and never claim that you changed files."
	defaultMaxTurns     = 12
	defaultMaxToolSteps = 32
)

// NewCommand assembles the production AICE command tree.
func NewCommand() (*cobra.Command, error) {
	return newCommand(dependencies{
		loadConfig: config.Load,
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
	loadConfig func() (config.Config, error)
	newModel   func(config.Config) (agent.Model, error)
	runTUI     func(context.Context, tui.Runner, tui.Options) error
}

func newCommand(dependencies dependencies) (*cobra.Command, error) {
	if dependencies.loadConfig == nil {
		return nil, fmt.Errorf("app: config loader is required")
	}
	if dependencies.newModel == nil {
		return nil, fmt.Errorf("app: model factory is required")
	}
	if dependencies.runTUI == nil {
		dependencies.runTUI = tui.Run
	}

	application := &application{dependencies: dependencies}
	return cli.NewRootCommand(cli.Dependencies{
		Printer:    application,
		Interactor: application,
	})
}

type application struct {
	dependencies dependencies
}

func (a *application) Print(
	ctx context.Context,
	request cli.PrintRequest,
	output io.Writer,
) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}

	environment, err := a.openEnvironment(request.Workspace)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, environment.Close())
	}()
	prompt, err := llm.NewUserMessage(llm.NewTextContent(request.Prompt).Part())
	if err != nil {
		return fmt.Errorf("app: create prompt: %w", err)
	}

	printer := &streamPrinter{output: output}
	_, loopErr := environment.loop.Run(ctx, agent.RunInput{
		Model:        deepseek.DefaultModel(),
		SystemPrompt: defaultSystemPrompt,
		Prompt:       prompt,
	}, printer.Accept)
	finishErr := printer.Finish()
	if loopErr != nil {
		return errors.Join(fmt.Errorf("app: run agent: %w", loopErr), finishErr)
	}
	return finishErr
}

// Interactive runs one multi-turn terminal session.
func (a *application) Interactive(
	ctx context.Context,
	request cli.InteractiveRequest,
) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if request.Input == nil {
		return fmt.Errorf("app: input is required")
	}
	if request.Output == nil {
		return fmt.Errorf("app: output is required")
	}

	environment, err := a.openEnvironment(request.Workspace)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, environment.Close())
	}()

	session := &interactiveSession{loop: environment.loop}
	if err := a.dependencies.runTUI(ctx, session, tui.Options{
		Input:  request.Input,
		Output: request.Output,
	}); err != nil {
		return fmt.Errorf("app: run TUI: %w", err)
	}
	return nil
}

type environment struct {
	loop      *agent.Loop
	workspace *tool.Workspace
}

func (a *application) openEnvironment(root string) (*environment, error) {
	configuration, err := a.dependencies.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("app: load configuration: %w", err)
	}
	model, err := a.dependencies.newModel(configuration)
	if err != nil {
		return nil, fmt.Errorf("app: create model: %w", err)
	}

	workspace, err := tool.NewWorkspace(root)
	if err != nil {
		return nil, fmt.Errorf("app: open workspace: %w", err)
	}
	tools, err := newReadOnlyTools(workspace)
	if err != nil {
		return nil, errors.Join(err, workspace.Close())
	}
	loop, err := agent.NewLoop(model, tools, agent.Limits{
		MaxTurns:     defaultMaxTurns,
		MaxToolSteps: defaultMaxToolSteps,
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("app: create agent loop: %w", err),
			workspace.Close(),
		)
	}
	return &environment{loop: loop, workspace: workspace}, nil
}

func (e *environment) Close() error {
	return e.workspace.Close()
}

type interactiveSession struct {
	loop    *agent.Loop
	history []llm.AgentMessage
}

func (s *interactiveSession) Run(
	ctx context.Context,
	promptText string,
	sink agent.EventSink,
) error {
	prompt, err := llm.NewUserMessage(llm.NewTextContent(promptText).Part())
	if err != nil {
		return fmt.Errorf("app: create prompt: %w", err)
	}
	result, runErr := s.loop.Run(ctx, agent.RunInput{
		Model:        deepseek.DefaultModel(),
		SystemPrompt: defaultSystemPrompt,
		History:      s.history,
		Prompt:       prompt,
	}, sink)
	s.history = append(s.history, result.Messages()...)
	if runErr != nil {
		return fmt.Errorf("app: run agent: %w", runErr)
	}
	return nil
}

func newReadOnlyTools(workspace *tool.Workspace) ([]agent.Tool, error) {
	read, err := tool.NewRead(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create read tool: %w", err)
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create ls tool: %w", err)
	}
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create grep tool: %w", err)
	}
	find, err := tool.NewFind(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create find tool: %w", err)
	}
	return []agent.Tool{read, ls, grep, find}, nil
}

type streamPrinter struct {
	output          io.Writer
	pendingText     bool
	lastWrittenByte byte
}

func (p *streamPrinter) Accept(ctx context.Context, event agent.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Type == agent.EventTypeMessageEnd && event.AssistantMessage != nil {
		return p.finishLine()
	}
	if event.Type != agent.EventTypeMessageUpdate || event.StreamEvent == nil {
		return nil
	}
	if event.StreamEvent.Type != llm.EventTypeTextDelta || event.StreamEvent.Delta == "" {
		return nil
	}

	delta := event.StreamEvent.Delta
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
