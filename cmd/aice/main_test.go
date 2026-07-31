package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestMain(m *testing.M) {
	testHome, err := os.MkdirTemp("", "aice-cmd-test-home-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(1)
	}

	environment := []struct {
		key   string
		value string
	}{
		{key: "HOME", value: testHome},
		{key: "USERPROFILE", value: testHome},
		{key: config.EnvDeepSeekAPIKey, value: ""},
		{
			key:   config.EnvDeepSeekBaseURL,
			value: "http://127.0.0.1:0",
		},
	}
	for _, variable := range environment {
		if err := os.Setenv(variable.key, variable.value); err != nil {
			_, _ = fmt.Fprintf(
				os.Stderr,
				"set isolated test environment %s: %v\n",
				variable.key,
				err,
			)
			_ = os.RemoveAll(testHome)
			os.Exit(1)
		}
	}

	code := m.Run()
	if err := os.RemoveAll(testHome); err != nil && code == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		code = 1
	}
	os.Exit(code)
}

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
	if got := run(t.Context(), []string{"inspect"}, output, errorOutput); got != 2 {
		t.Fatalf("run() = %d, want 2", got)
	}
	if !strings.Contains(errorOutput.String(), "unknown command") {
		t.Errorf("stderr = %q, want interactive argument guidance", errorOutput.String())
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
