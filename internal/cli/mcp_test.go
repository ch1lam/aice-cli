package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type mcpManagerRecorder struct {
	calls   int
	request cli.MCPRequest
}

func (m *mcpManagerRecorder) ManageMCP(_ context.Context, r cli.MCPRequest) (interaction.MCPResult, error) {
	m.calls++
	m.request = r
	return interaction.MCPResult{Committed: true, Message: "saved"}, nil
}

func TestMCPCLISecretUsesStdinAndExactBinding(t *testing.T) {
	manager := &mcpManagerRecorder{}
	command, err := cli.NewRootCommand(cli.Dependencies{Printer: &recordingPrinter{}, Interactor: &recordingInteractor{}, Compactor: &recordingCompactor{}, Navigator: &recordingNavigator{}, Configurator: &apiKeyRecorder{}, MCPManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	command.SetArgs([]string{"mcp", "credential", "user:docs", "token", "--fingerprint", fingerprint, "--workspace", "/fixture", "--trust-project"})
	command.SetIn(strings.NewReader("private-value\n"))
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := manager.request
	if manager.calls != 1 || r.Workspace != "/fixture" || r.ProjectTrustOverride == nil || !*r.ProjectTrustOverride || r.Operation.Secret != "private-value" || r.Operation.Fingerprint != fingerprint || r.Operation.Slot != "token" || strings.Contains(out.String(), "private-value") {
		t.Fatal("management boundary changed secret or identity")
	}
}

func TestMCPCLIRejectsInvalidInputBeforeManagement(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		input string
	}{
		{[]string{"mcp", "approve", "user:docs"}, ""},
		{[]string{"mcp", "login", "user:docs"}, ""},
		{[]string{"mcp", "logout", "user:docs"}, ""},
		{[]string{"mcp", "credential", "user:docs", "token", "--fingerprint", strings.Repeat("a", 64)}, strings.Repeat("x", 8193)},
		{[]string{"mcp", "credential", "user:docs", "token", "--fingerprint", strings.Repeat("a", 64)}, "\n"},
		{[]string{"mcp", "status", "--trust-project", "--no-trust-project"}, ""},
		{[]string{"mcp", "status", "--workspace", " "}, ""},
	} {
		manager := &mcpManagerRecorder{}
		command, err := cli.NewRootCommand(cli.Dependencies{Printer: &recordingPrinter{}, Interactor: &recordingInteractor{}, Compactor: &recordingCompactor{}, Navigator: &recordingNavigator{}, Configurator: &apiKeyRecorder{}, MCPManager: manager})
		if err != nil {
			t.Fatal(err)
		}
		command.SetArgs(tc.args)
		command.SetIn(strings.NewReader(tc.input))
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.ExecuteContext(t.Context()); err == nil || manager.calls != 0 {
			t.Fatalf("invalid input reached manager: %v", tc.args)
		}
	}
}

func TestMCPCLILoginCarriesTransientOutputAndBrowserPreference(t *testing.T) {
	manager := &mcpManagerRecorder{}
	command, err := cli.NewRootCommand(cli.Dependencies{Printer: &recordingPrinter{}, Interactor: &recordingInteractor{}, Compactor: &recordingCompactor{}, Navigator: &recordingNavigator{}, Configurator: &apiKeyRecorder{}, MCPManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"mcp", "login", "user:docs", "--fingerprint", strings.Repeat("a", 64), "--no-browser"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !manager.request.NoBrowser || manager.request.Auth == nil || manager.request.Auth.Notify == nil || manager.request.Auth.Input != nil || manager.request.Operation.Action != "login" {
		t.Fatal("CLI login interaction was not scoped correctly")
	}
}
