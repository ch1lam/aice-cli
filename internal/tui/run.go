// Package tui implements AICE's Bubble Tea terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const runUpdateBuffer = 32

type RunInput = interaction.RunInput
type ActiveRun = interaction.ActiveRun
type Runner = interaction.Runner
type RuntimeState = interaction.RuntimeState
type RuntimeStateProvider = interaction.RuntimeStateProvider
type SideThreadFactory = interaction.SideThreadFactory

// Options contains the terminal streams and model state shown by the program.
type Options struct {
	Input            io.Reader
	Output           io.Writer
	Model            DisplayModel
	Thinking         DisplayThinking
	APIKeyConfigured bool
	Usage            DisplayUsage
	WorkingDirectory string
	// Version is shown on the welcome screen only; it stays off the
	// transcript while the conversation is in use.
	Version string
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

	var sideRequests chan runRequest
	var sideControllerDone chan struct{}
	if factory, ok := runner.(SideThreadFactory); ok {
		sideRequests = make(chan runRequest)
		sideControllerDone = make(chan struct{})
		go serveSideRuns(
			controllerCtx,
			factory,
			sideRequests,
			sideControllerDone,
		)
	}
	defer func() {
		stopController()
		<-controllerDone
		if sideControllerDone != nil {
			<-sideControllerDone
		}
	}()

	var slashCommands []SlashCommand
	if commandRunner, ok := runner.(SlashCommandRunner); ok {
		slashCommands = commandRunner.SlashCommands()
	}
	if sideRequests != nil {
		slashCommands = append(slashCommands, btwSlashCommand())
	}
	initialModel := newModel(requests, controllerDone, slashCommands...)
	initialModel.sideRequests = sideRequests
	initialModel.sideControllerDone = sideControllerDone
	initialModel.currentModel = options.Model
	initialModel.thinking = options.Thinking
	initialModel.apiKeyConfigured = options.APIKeyConfigured
	initialModel.sessionUsage = options.Usage
	initialModel.workingDirectory = options.WorkingDirectory
	initialModel.version = options.Version
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
	event    DisplayEvent
	active   ActiveRun
	cancel   context.CancelFunc
	err      error
	output   string
	state    *RuntimeState
	commands *[]SlashCommand
	done     bool
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

func serveSideRuns(
	ctx context.Context,
	factory SideThreadFactory,
	requests <-chan runRequest,
	done chan<- struct{},
) {
	defer close(done)
	var runner Runner
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			if runner == nil {
				var err error
				runner, err = factory.NewSideThread()
				if err != nil {
					finishUnstartedRun(ctx, request, err)
					continue
				}
			}
			runOne(ctx, runner, request)
		}
	}
}

func finishUnstartedRun(
	ctx context.Context,
	request runRequest,
	err error,
) {
	defer close(request.updates)
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:  err,
		done: true,
	})
}

func runOne(ctx context.Context, runner Runner, request runRequest) {
	defer close(request.updates)

	if request.command != nil {
		runSlashCommand(ctx, runner, request)
		return
	}

	active, err := runner.NewRun(RunInput{Prompt: request.prompt}, func(
		eventCtx context.Context,
		event DisplayEvent,
	) error {
		if !sendRunUpdate(eventCtx, request.updates, runUpdate{event: event}) {
			return eventCtx.Err()
		}
		return nil
	})
	if err != nil {
		state, commands := runnerSnapshots(runner)
		_ = sendRunUpdate(ctx, request.updates, runUpdate{
			err:      err,
			state:    state,
			commands: commands,
			done:     true,
		})
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	if !sendRunUpdate(ctx, request.updates, runUpdate{
		active: active,
		cancel: cancel,
	}) {
		cancel()
		return
	}
	err = active.Run(runCtx)
	cancel()

	state, commands := runnerSnapshots(runner)
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:      err,
		state:    state,
		commands: commands,
		done:     true,
	})
}

func runSlashCommand(ctx context.Context, runner Runner, request runRequest) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !sendRunUpdate(ctx, request.updates, runUpdate{cancel: cancel}) {
		return
	}

	var output string
	var err error
	commandRunner, ok := runner.(SlashCommandRunner)
	if !ok {
		err = fmt.Errorf("tui: slash command runner is required")
	} else {
		output, err = commandRunner.RunSlashCommand(runCtx, *request.command)
	}
	state, commands := runnerSnapshots(runner)
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:      err,
		output:   output,
		state:    state,
		commands: commands,
		done:     true,
	})
}

func runnerSnapshots(runner Runner) (*RuntimeState, *[]SlashCommand) {
	var state *RuntimeState
	if provider, ok := runner.(RuntimeStateProvider); ok {
		snapshot := provider.RuntimeState()
		state = &snapshot
	}
	var commands *[]SlashCommand
	if commandRunner, ok := runner.(SlashCommandRunner); ok {
		snapshot := commandRunner.SlashCommands()
		commands = &snapshot
	}
	return state, commands
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
