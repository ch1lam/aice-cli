package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	errorOutput := new(bytes.Buffer)
	if got := run(t.Context(), []string{"--help"}, output, errorOutput); got != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", got, errorOutput.String())
	}
	if !strings.Contains(output.String(), "--print") {
		t.Errorf("help output = %q, want --print flag", output.String())
	}
	if got := errorOutput.String(); got != "" {
		t.Errorf("help stderr = %q, want empty", got)
	}
}

func TestRunUsageError(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	errorOutput := new(bytes.Buffer)
	if got := run(t.Context(), nil, output, errorOutput); got != 2 {
		t.Fatalf("run() = %d, want 2", got)
	}
	if !strings.Contains(errorOutput.String(), "use --print") {
		t.Errorf("stderr = %q, want print-mode guidance", errorOutput.String())
	}
}

func TestRunConfigurationError(t *testing.T) {
	t.Setenv(config.EnvDeepSeekAPIKey, "")

	output := new(bytes.Buffer)
	errorOutput := new(bytes.Buffer)
	if got := run(
		t.Context(),
		[]string{"--print", "inspect"},
		output,
		errorOutput,
	); got != 1 {
		t.Fatalf("run() = %d, want 1", got)
	}
	if !strings.Contains(errorOutput.String(), config.EnvDeepSeekAPIKey) {
		t.Errorf("stderr = %q, want missing API key name", errorOutput.String())
	}
	if got := output.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}
