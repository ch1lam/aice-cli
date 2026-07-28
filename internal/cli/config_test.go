package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
)

func TestConfigSetKeyReadsStdinAndPersistsCredential(t *testing.T) {
	t.Parallel()

	configurator := &apiKeyRecorder{path: "/global/auth.json"}
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: configurator,
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	output := new(bytes.Buffer)
	command.SetIn(strings.NewReader(" test-key \n"))
	command.SetOut(output)
	command.SetArgs([]string{"config", "set-key"})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if configurator.request.APIKey != "test-key" {
		t.Errorf(
			"API key request = %q, want trimmed key",
			configurator.request.APIKey,
		)
	}
	if got := output.String(); !strings.Contains(got, configurator.path) {
		t.Errorf("output = %q, want auth path", got)
	}
	if strings.Contains(output.String(), "test-key") {
		t.Errorf("output exposes API key: %q", output.String())
	}
}

func TestConfigSetKeyRejectsBlankInput(t *testing.T) {
	t.Parallel()

	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &apiKeyRecorder{},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader(" \n"))
	command.SetArgs([]string{"config", "set-key"})

	err = command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "must not be blank") {
		t.Fatalf("ExecuteContext() error = %v, want blank key error", err)
	}
	if got := cli.ExitCode(err); got != 2 {
		t.Errorf("ExitCode() = %d, want 2", got)
	}
}

func TestConfigSetKeyReturnsPersistenceError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("auth unavailable")
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &apiKeyRecorder{err: wantErr},
	})
	if err != nil {
		t.Fatalf("NewRootCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader("test-key\n"))
	command.SetArgs([]string{"config", "set-key"})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
}

type apiKeyRecorder struct {
	request cli.APIKeyRequest
	path    string
	err     error
}

func (r *apiKeyRecorder) SaveAPIKey(
	_ context.Context,
	request cli.APIKeyRequest,
) (string, error) {
	r.request = request
	return r.path, r.err
}
