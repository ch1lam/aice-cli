package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/spf13/cobra"
)

func TestUpdateCommandRunsUpdater(t *testing.T) {
	t.Parallel()

	updater := &recordingUpdater{}
	command := newCommandWithUpdater(t, updater)
	command.SetOut(new(bytes.Buffer))
	command.SetArgs([]string{"update"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if updater.request != (cli.UpdateRequest{}) {
		t.Errorf("Update() request = %#v, want zero request", updater.request)
	}
}

func TestUpdateCommandForwardsFlags(t *testing.T) {
	t.Parallel()

	updater := &recordingUpdater{}
	command := newCommandWithUpdater(t, updater)
	command.SetOut(new(bytes.Buffer))
	command.SetArgs([]string{"update", "--check", "--force"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	want := cli.UpdateRequest{Check: true, Force: true}
	if updater.request != want {
		t.Errorf("Update() request = %#v, want %#v", updater.request, want)
	}
}

func TestUpdateCommandRejectsPositionalArgument(t *testing.T) {
	t.Parallel()

	updater := &recordingUpdater{}
	command := newCommandWithUpdater(t, updater)
	command.SetArgs([]string{"update", "v1.2.3"})

	err := command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("ExecuteContext() error = %v, want unknown command", err)
	}
	if got := cli.ExitCode(err); got != 2 {
		t.Errorf("ExitCode() = %d, want 2", got)
	}
	if updater.request != (cli.UpdateRequest{}) {
		t.Errorf("Update() called with %#v despite usage error", updater.request)
	}
}

func TestUpdateCommandMissingWithoutUpdater(t *testing.T) {
	t.Parallel()

	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetArgs([]string{"update"})

	err = command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("ExecuteContext() error = %v, want unknown command", err)
	}
}

func TestUpdateCommandReturnsUpdaterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("release server unavailable")
	updater := &recordingUpdater{err: wantErr}
	command := newCommandWithUpdater(t, updater)
	command.SetArgs([]string{"update"})

	err := command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Errorf("ExitCode() = %d, want 1", got)
	}
}

func newCommandWithUpdater(t *testing.T, updater cli.Updater) *cobra.Command {
	t.Helper()
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
		Updater:      updater,
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	return command
}

type recordingUpdater struct {
	request cli.UpdateRequest
	err     error
}

func (u *recordingUpdater) Update(
	_ context.Context,
	request cli.UpdateRequest,
	_ io.Writer,
) error {
	u.request = request
	return u.err
}
