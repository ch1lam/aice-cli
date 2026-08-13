// Package tui implements AICE's Bubble Tea terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const runUpdateBuffer = 32

type RunInput = interaction.RunInput
type ActiveRun = interaction.ActiveRun
type Runner = interaction.Runner
type RuntimeState = interaction.RuntimeState
type RuntimeStateProvider = interaction.RuntimeStateProvider
type SideThread = interaction.SideThread
type SideThreadManager = interaction.SideThreadManager

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
	if manager, ok := runner.(SideThreadManager); ok {
		sideRequests = make(chan runRequest)
		sideControllerDone = make(chan struct{})
		go serveSideRuns(
			controllerCtx,
			manager,
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
	if manager, ok := runner.(SideThreadManager); ok {
		initialModel.side.manager = manager
	}
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
	// sideCreate requests a brand-new side thread for this prompt;
	// sideThreadID targets an existing thread for a follow-up. The resolved
	// metadata is stored back on sideThread before the run starts.
	sideCreate   bool
	sideThreadID uint64
	sideThread   *SideThread
}

type runUpdate struct {
	event    DisplayEvent
	active   ActiveRun
	cancel   context.CancelFunc
	err      error
	output   string
	state    *RuntimeState
	commands *[]SlashCommand
	// sideThread is set on the first update of a side run and identifies the
	// registry thread the run belongs to.
	sideThread *SideThread
	done       bool
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
			_ = runOne(ctx, runner, request)
		}
	}
}

// serveSideRuns owns the side-thread controller. Every request is prepared
// (created or opened through the manager) and then executed on its own
// goroutine, so independent threads can answer concurrently. The registry in
// internal/app serializes runs per thread and caps global concurrency; the
// controller only carries each run to its own event channel.
func serveSideRuns(
	ctx context.Context,
	manager SideThreadManager,
	requests <-chan runRequest,
	done chan<- struct{},
) {
	defer close(done)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			wg.Add(1)
			go runSideRequest(ctx, manager, request, &wg)
		}
	}
}

// runSideRequest resolves the thread for one side run, then executes it.
// Resolution failures are delivered as terminal updates; the model restores
// drafts and shows the reason. runOne owns and closes the update channel on
// success.
func runSideRequest(
	ctx context.Context,
	manager SideThreadManager,
	request runRequest,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	var thread SideThread
	var runner Runner
	var err error
	if request.sideCreate {
		thread, runner, err = manager.CreateSideThread(request.prompt)
	} else {
		thread, runner, err = manager.OpenSideThread(request.sideThreadID)
	}
	if err != nil {
		_ = sendRunUpdate(ctx, request.updates, runUpdate{
			err:  err,
			done: true,
		})
		close(request.updates)
		return
	}
	request.sideThread = &thread
	_ = runOne(ctx, runner, request)
}

// runOne prepares and executes one run request, delivering every lifecycle
// update on request.updates. It returns the run's terminal error so callers
// that need to react to a failed run (such as the side controller forgetting
// an unusable thread id) can do so; the main controller discards it.
func runOne(ctx context.Context, runner Runner, request runRequest) error {
	defer close(request.updates)

	if request.command != nil {
		return runSlashCommand(ctx, runner, request)
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
			err:        err,
			state:      state,
			commands:   commands,
			sideThread: request.sideThread,
			done:       true,
		})
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	if !sendRunUpdate(ctx, request.updates, runUpdate{
		active:     active,
		cancel:     cancel,
		sideThread: request.sideThread,
	}) {
		cancel()
		return ctx.Err()
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
	return err
}

func runSlashCommand(ctx context.Context, runner Runner, request runRequest) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !sendRunUpdate(ctx, request.updates, runUpdate{cancel: cancel}) {
		return ctx.Err()
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
	return err
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
