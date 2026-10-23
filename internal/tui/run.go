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
	"github.com/ch1lam/aice-cli/internal/llm"
)

const runUpdateBuffer = 32

// Options contains the terminal streams and model state shown by the program.
type Options struct {
	Transcript *interaction.Transcript
	// StartupNotice reports a recoverable configuration issue in the transcript.
	StartupNotice    string
	Input            io.Reader
	Output           io.Writer
	Model            interaction.DisplayModel
	Thinking         interaction.DisplayThinking
	APIKeyConfigured bool
	Usage            interaction.DisplayUsage
	Context          interaction.DisplayContext
	WorkingDirectory string
	// Version is shown on the welcome screen only; it stays off the
	// transcript while the conversation is in use.
	Version string
}

// Run starts an interactive Bubble Tea program and owns its run controller.
func Run(ctx context.Context, frontend interaction.Frontend, options Options) error {
	if ctx == nil {
		return fmt.Errorf("tui: context is required")
	}
	if frontend.Runner == nil {
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
	go serveRuns(controllerCtx, frontend, requests, controllerDone)

	var sideRequests chan runRequest
	var sideControllerDone chan struct{}
	if frontend.SideThreads != nil {
		sideRequests = make(chan runRequest)
		sideControllerDone = make(chan struct{})
		go serveSideRuns(
			controllerCtx,
			frontend.SideThreads,
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

	var slashCommands []interaction.Command
	if frontend.Commands != nil {
		slashCommands = frontend.Commands.SlashCommands()
	}
	if sideRequests != nil {
		slashCommands = append(slashCommands, btwSlashCommand())
	}
	initialModel := newModel(requests, controllerDone, slashCommands...)
	if frontend.Sessions != nil {
		var closeQueries func()
		initialModel.searchSessions, initialModel.previewSession, initialModel.readSession, initialModel.renameSession, closeQueries = sessionBrowserCommands(controllerCtx, frontend.Sessions)
		defer closeQueries()
	}
	if frontend.Usage != nil {
		var closeUsage func()
		initialModel.readUsage, closeUsage = usageCommands(controllerCtx, frontend.Usage)
		defer closeUsage()
	}
	if reader := frontend.Settings; reader != nil {
		if frontend.SettingsStatus != nil {
			var closeStatus func()
			initialModel.readSettingsStatus, closeStatus = settingsStatusCommands(controllerCtx, frontend.SettingsStatus)
			defer closeStatus()
		}
		if frontend.SettingsActions != nil {
			var closeActions func()
			initialModel.runSettingsAction, closeActions = settingsActionCommands(controllerCtx, frontend.SettingsActions, reader)
			defer closeActions()
		}
		if frontend.SettingsWriter != nil {
			var closeSettings func()
			initialModel.readSettings, initialModel.writeSettings, closeSettings = settingsCommands(controllerCtx, reader, frontend.SettingsWriter)
			defer closeSettings()
		}
	}
	if options.Transcript != nil {
		initialModel.replaceTranscript(options.Transcript)
	}
	if frontend.Files != nil {
		var closeCompletion func()
		initialModel.completeFiles, closeCompletion = fileCompletionCommand(controllerCtx, frontend.Files)
		defer closeCompletion()
	}
	initialModel.prepareDelivery = deliveryCommand(controllerCtx)
	initialModel.clipboard = pasteClipboard(controllerCtx)
	initialModel.openDirectory = func(path string) tea.Cmd {
		return openDirectoryCommand(controllerCtx, path)
	}
	initialModel.sideRequests = sideRequests
	initialModel.sideControllerDone = sideControllerDone
	initialModel.side.manager = frontend.SideThreads
	if frontend.Guards != nil {
		initialModel.guardRequests = frontend.Guards.GuardRequests()
	}
	if frontend.Questions != nil {
		initialModel.questionRequests = frontend.Questions.QuestionRequests()
	}
	if options.StartupNotice != "" {
		initialModel.entries = append(initialModel.entries, transcriptEntry{kind: entryNotice, text: options.StartupNotice})
	}
	initialModel.currentModel = options.Model
	initialModel.thinking = options.Thinking
	initialModel.apiKeyConfigured = options.APIKeyConfigured
	initialModel.sessionUsage = options.Usage
	initialModel.contextUsage = options.Context
	initialModel.workingDirectory = options.WorkingDirectory
	initialModel.version = options.Version
	if frontend.CheckUpdate != nil {
		initialModel.updateCheck = checkForUpdate(
			controllerCtx,
			frontend.CheckUpdate,
		)
		initialModel.welcomeUpdate.state = welcomeUpdateChecking
	}
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

func checkForUpdate(ctx context.Context, checker interaction.UpdateChecker) tea.Cmd {
	return func() tea.Msg {
		result, err := checker(ctx)
		return updateCheckMsg{result: result, err: err}
	}
}

type runRequest struct {
	continuation *interaction.TaskContinuation
	prompt       string
	files        []string
	skills       []string
	images       []llm.ImageContent
	command      *interaction.CommandRequest
	updates      chan runUpdate
	// sideCreate requests a brand-new side thread for this prompt;
	// sideThreadID targets an existing thread for a follow-up. The resolved
	// metadata is stored back on sideThread before the run starts.
	sideCreate   bool
	sideThreadID uint64
	sideThread   *interaction.SideThread
}

type runUpdate struct {
	auth     *interaction.AuthPrompt
	event    interaction.Event
	active   interaction.ActiveRun
	cancel   context.CancelFunc
	err      error
	output   string
	state    *interaction.RuntimeState
	commands *[]interaction.Command
	// sideThread is set on the first update of a side run and identifies the
	// registry thread the run belongs to.
	sideThread *interaction.SideThread
	done       bool
}

func serveRuns(
	ctx context.Context,
	frontend interaction.Frontend,
	requests <-chan runRequest,
	done chan<- struct{},
) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			runOne(ctx, frontend, request)
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
	manager interaction.SideThreadManager,
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
	manager interaction.SideThreadManager,
	request runRequest,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	var thread interaction.SideThread
	var runner interaction.Runner
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
	// Side runs expose no commands or runtime state to refresh.
	runOne(ctx, interaction.Frontend{Runner: runner}, request)
}

// runOne prepares and executes one run request, delivering every lifecycle
// update, including the terminal error, on request.updates.
func runOne(ctx context.Context, frontend interaction.Frontend, request runRequest) {
	defer close(request.updates)

	if request.command != nil {
		runSlashCommand(ctx, frontend, request)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !sendRunUpdate(ctx, request.updates, runUpdate{cancel: cancel, sideThread: request.sideThread}) {
		return
	}
	active, err := frontend.Runner.NewRun(runCtx, interaction.RunInput{Prompt: request.prompt, Images: request.images, Files: request.files, Skills: request.skills, Continuation: request.continuation}, func(
		eventCtx context.Context,
		event interaction.Event,
	) error {
		if !sendRunUpdate(eventCtx, request.updates, runUpdate{event: event}) {
			return eventCtx.Err()
		}
		return nil
	})
	if err != nil {
		state, commands := frontendSnapshots(frontend)
		_ = sendRunUpdate(ctx, request.updates, runUpdate{
			err:        err,
			state:      state,
			commands:   commands,
			sideThread: request.sideThread,
			done:       true,
		})
		return
	}

	if !sendRunUpdate(ctx, request.updates, runUpdate{
		active:     active,
		cancel:     cancel,
		sideThread: request.sideThread,
	}) {
		cancel()
		return
	}
	err = active.Run(runCtx)
	cancel()

	state, commands := frontendSnapshots(frontend)
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:      err,
		state:    state,
		commands: commands,
		done:     true,
	})
}

func runSlashCommand(ctx context.Context, frontend interaction.Frontend, request runRequest) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !sendRunUpdate(ctx, request.updates, runUpdate{cancel: cancel}) {
		return
	}

	var output string
	var err error
	if frontend.Commands == nil {
		err = fmt.Errorf("tui: slash command runner is required")
	} else {
		command := *request.command
		if command.Auth != nil {
			command.Auth = &interaction.AuthInteraction{Input: command.Auth.Input,
				Notify: func(ctx context.Context, prompt interaction.AuthPrompt) error {
					if !sendRunUpdate(ctx, request.updates, runUpdate{auth: &prompt}) {
						return ctx.Err()
					}
					return nil
				},
			}
		}
		output, err = frontend.Commands.RunSlashCommand(runCtx, command)
	}
	state, commands := frontendSnapshots(frontend)
	_ = sendRunUpdate(ctx, request.updates, runUpdate{
		err:      err,
		output:   output,
		state:    state,
		commands: commands,
		done:     true,
	})
}

func frontendSnapshots(frontend interaction.Frontend) (*interaction.RuntimeState, *[]interaction.Command) {
	var state *interaction.RuntimeState
	if frontend.State != nil {
		snapshot := frontend.State.RuntimeState()
		state = &snapshot
	}
	var commands *[]interaction.Command
	if frontend.Commands != nil {
		snapshot := frontend.Commands.SlashCommands()
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
