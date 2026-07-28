// Package tui implements AICE's Bubble Tea terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const runUpdateBuffer = 32

// Runner executes one prompt and emits its ordered Agent Loop events.
// Implementations may retain conversation history between calls.
type Runner interface {
	Run(ctx context.Context, prompt string, sink agent.AgentEventSink) error
}

// Options contains the terminal streams and model state shown by the program.
type Options struct {
	Input            io.Reader
	Output           io.Writer
	Model            llm.Model
	Thinking         llm.ThinkingLevel
	Usage            llm.Usage
	WorkingDirectory string
}

// Run starts an interactive Bubble Tea program and owns its run controller.
func Run(ctx context.Context, runner Runner, options Options) error {
	if ctx == nil {
		return fmt.Errorf("tui: context is required")
	}
	if runner == nil {
		return fmt.Errorf("tui: runner is required")
	}
	if options.Input == nil {
		return fmt.Errorf("tui: input is required")
	}
	if options.Output == nil {
		return fmt.Errorf("tui: output is required")
	}
	if options.Model.ID == "" {
		return fmt.Errorf("tui: model ID is required")
	}
	if strings.TrimSpace(options.WorkingDirectory) == "" {
		return fmt.Errorf("tui: working directory is required")
	}

	controllerCtx, stopController := context.WithCancel(ctx)
	requests := make(chan runRequest)
	controllerDone := make(chan struct{})
	go serveRuns(controllerCtx, runner, requests, controllerDone)
	defer func() {
		stopController()
		<-controllerDone
	}()

	var slashCommands []SlashCommand
	if commandRunner, ok := runner.(SlashCommandRunner); ok {
		slashCommands = commandRunner.SlashCommands()
	}
	initialModel := newModel(requests, controllerDone, slashCommands...)
	initialModel.currentModel = options.Model
	initialModel.thinking = options.Thinking
	initialModel.sessionUsage = options.Usage
	initialModel.workingDirectory = options.WorkingDirectory
	program := tea.NewProgram(
		initialModel,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
		tea.WithoutSignalHandler(),
	)
	if _, err := program.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, tea.ErrInterrupted) {
			return context.Canceled
		}
		return fmt.Errorf("tui: run program: %w", err)
	}
	return nil
}

type runRequest struct {
	prompt  string
	command *SlashCommandRequest
	updates chan runUpdate
}

type runUpdate struct {
	event  agent.AgentEvent
	cancel context.CancelFunc
	err    error
	output string
	done   bool
}

func serveRuns(
	ctx context.Context,
	runner Runner,
	requests <-chan runRequest,
	done chan<- struct{},
) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			runOne(ctx, runner, request)
		}
	}
}

func runOne(ctx context.Context, runner Runner, request runRequest) {
	defer close(request.updates)

	runCtx, cancel := context.WithCancel(ctx)
	if !sendRunUpdate(ctx, request.updates, runUpdate{cancel: cancel}) {
		cancel()
		return
	}

	var output string
	var err error
	if request.command != nil {
		commandRunner, ok := runner.(SlashCommandRunner)
		if !ok {
			err = fmt.Errorf("tui: slash command runner is required")
		} else {
			output, err = commandRunner.RunSlashCommand(runCtx, *request.command)
		}
	} else {
		err = runner.Run(runCtx, request.prompt, func(
			eventCtx context.Context,
			event agent.AgentEvent,
		) error {
			if !sendRunUpdate(eventCtx, request.updates, runUpdate{event: event}) {
				return eventCtx.Err()
			}
			return nil
		})
	}
	cancel()
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:    err,
		output: output,
		done:   true,
	})
}

func sendRunUpdate(
	ctx context.Context,
	updates chan<- runUpdate,
	update runUpdate,
) bool {
	select {
	case <-ctx.Done():
		return false
	case updates <- update:
		return true
	}
}
