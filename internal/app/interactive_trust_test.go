package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveTrustSavesDecision(t *testing.T) {
	t.Parallel()

	workspacePath := canonicalTestWorkspace(t)
	store := trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
	runner := &interactiveSession{
		trustStore:    store,
		workspacePath: workspacePath,
	}
	choices := trust.Choices(workspacePath)

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "trust",
		Arguments: "0",
	})
	if err != nil {
		t.Fatalf("/trust error = %v", err)
	}
	if !strings.Contains(output, "saved") {
		t.Errorf("/trust output = %q, want saved message", output)
	}
	entry, found, err := store.Lookup(workspacePath)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Decision != trust.DecisionTrusted {
		t.Errorf("stored decision = %#v, want trusted", entry)
	}
	if len(choices) < 1 || choices[0].Decision != trust.DecisionTrusted {
		t.Fatalf("trust menu first choice = %#v, want Trust", choices[0])
	}
}

func TestInteractiveTrustSessionOnlyDoesNotPersist(t *testing.T) {
	t.Parallel()

	workspacePath := canonicalTestWorkspace(t)
	store := trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
	runner := &interactiveSession{
		trustStore:    store,
		workspacePath: workspacePath,
	}

	// "Trust (this session only)" is option 2 when the parent option exists.
	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "trust",
		Arguments: "2",
	})
	if err != nil {
		t.Fatalf("/trust error = %v", err)
	}
	if !strings.Contains(output, "this Session only") {
		t.Errorf("/trust output = %q, want session-only message", output)
	}
	if _, found, err := store.Lookup(workspacePath); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if found {
		t.Fatal("session-only choice persisted to the trust store")
	}
}

func TestInteractiveTrustParentPersistsParentAndClearsExact(t *testing.T) {
	t.Parallel()

	workspacePath := canonicalTestWorkspace(t)
	store := trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
	if err := store.Set(workspacePath, trust.DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	runner := &interactiveSession{
		trustStore:    store,
		workspacePath: workspacePath,
	}

	// "Trust parent folder" is option 1.
	if _, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "trust",
		Arguments: "1",
	}); err != nil {
		t.Fatalf("/trust error = %v", err)
	}
	entry, found, err := store.Lookup(workspacePath)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Path != filepath.Dir(workspacePath) {
		t.Errorf("parent decision = %#v, want inherited parent", entry)
	}
}

func canonicalTestWorkspace(t *testing.T) string {
	t.Helper()
	path, err := trust.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	return path
}

func TestInteractiveTrustRejectsInvalidChoice(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{
		trustStore:    trust.NewStore(filepath.Join(t.TempDir(), "trust.json")),
		workspacePath: t.TempDir(),
	}
	for _, arguments := range []string{"", "x", "-1", "99"} {
		_, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
			Name:      "trust",
			Arguments: arguments,
		})
		if err == nil {
			t.Errorf("/trust %q error = nil, want error", arguments)
		}
	}
}

func TestInteractiveSettingsShowsTrust(t *testing.T) {
	t.Parallel()

	paths := trustTestPaths(t)
	configuration := trustTestConfig(paths)
	runner := &interactiveSession{
		configuration: configuration,
		model:         deepseekModel(t, deepseek.ModelV4Flash),
		trustStore:    trust.NewStore(paths.GlobalTrust),
		trustDecision: trust.DecisionTrusted,
		trustSource:   trust.SourceStore,
		workspacePath: t.TempDir(),
	}
	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "settings",
	})
	if err != nil {
		t.Fatalf("/settings error = %v", err)
	}
	for _, want := range []string{
		"Default project trust: ask",
		"Project trust: trusted (saved decision)",
		"Trust store: " + paths.GlobalTrust,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("/settings output = %q, want %q", output, want)
		}
	}
}

func TestTrustMenuExposesFiveChoices(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{workspacePath: t.TempDir()}
	menu := runner.trustMenu()
	if menu == nil || menu.Title != "Project trust" {
		t.Fatalf("trustMenu() = %#v, want project trust menu", menu)
	}
	if len(menu.Options) != 5 {
		t.Fatalf("trust menu options = %d, want 5", len(menu.Options))
	}
	if got := menu.Options[0].Arguments; got != "0" {
		t.Errorf("first option argument = %q, want 0", got)
	}
	if got := menu.Options[4].Arguments; got != "4" {
		t.Errorf("last option argument = %q, want 4", got)
	}
}

func deepseekModel(t *testing.T, id string) llm.Model {
	t.Helper()
	for _, model := range deepseek.Models() {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("model %q not found", id)
	return llm.Model{}
}
