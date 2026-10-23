package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/trust"
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

	output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{
		Name:      "trust",
		Arguments: "0",
	})
	if err != nil {
		t.Fatalf("/trust error = %v", err)
	}
	if !strings.Contains(output, "saved") || !strings.Contains(output, "Restart AICE") {
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

func TestInteractiveTrustRejectsTemporaryChoices(t *testing.T) {
	t.Parallel()
	workspacePath := canonicalTestWorkspace(t)
	store := trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
	runner := &interactiveSession{trustStore: store, workspacePath: workspacePath}
	for index, choice := range trust.Choices(workspacePath) {
		if len(choice.Updates) != 0 {
			continue
		}
		output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{
			Name: "trust", Arguments: strconv.Itoa(index),
		})
		if err == nil || output != "" {
			t.Fatalf("temporary choice %q output = %q, error = %v, want rejection", choice.Label, output, err)
		}
	}
	if _, found, err := store.Lookup(workspacePath); err != nil || found {
		t.Fatalf("rejected temporary choices changed trust store: found = %v, error = %v", found, err)
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
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{
		Name:      "trust",
		Arguments: "1",
	}); err != nil {
		t.Fatalf("/trust error = %v", err)
	}
	entry, found, err := store.Lookup(workspacePath)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || !strings.EqualFold(entry.Path, filepath.Dir(workspacePath)) {
		t.Errorf("parent decision = %#v, want inherited parent", entry)
	}
}

func canonicalTestWorkspace(t *testing.T) string {
	t.Helper()
	path, err := hostpath.Canonical(t.TempDir())
	if err != nil {
		t.Fatalf("Canonical() error = %v", err)
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
		_, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{
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
	runner := &interactiveSession{
		configuration: trustTestConfig(paths),
		model:         deepseekModel(t, deepseek.ModelV4Flash),
		trustDecision: trust.DecisionTrusted,
		trustSource:   trust.SourceStore,
		workspacePath: t.TempDir(),
	}
	snapshot, err := runner.ReadSettings(t.Context())
	if err != nil {
		t.Fatalf("ReadSettings() error = %v", err)
	}
	for _, field := range snapshot.Fields {
		if field.ID == "project.loaded" {
			if want := fmt.Sprintf("%v (%v)", trust.DecisionTrusted, trust.SourceStore); field.Description != want {
				t.Fatalf("loaded trust = %q, want %q", field.Description, want)
			}
			return
		}
	}
	t.Fatal("settings have no loaded project trust")
}

func TestTrustMenuExposesOnlySavedChoices(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{workspacePath: t.TempDir()}
	menu := runner.trustMenu()
	if menu == nil || menu.Title != "Project trust" {
		t.Fatalf("trustMenu() = %#v, want project trust menu", menu)
	}
	if len(menu.Options) != 3 {
		t.Fatalf("trust menu options = %d, want 3", len(menu.Options))
	}
	if got := menu.Options[0].Arguments; got != "0" {
		t.Errorf("first option argument = %q, want 0", got)
	}
	if got := menu.Options[2].Arguments; got != "3" {
		t.Errorf("last option argument = %q, want 3", got)
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

func TestStartupTemporaryTrustAffectsPromptWithoutPersisting(t *testing.T) {
	t.Parallel()
	for _, decision := range []trust.Decision{trust.DecisionTrusted, trust.DecisionUntrusted} {
		t.Run(string(decision), func(t *testing.T) {
			workspace := t.TempDir()
			writeAppFile(t, workspace, "AGENTS.md", "temporary project guidance")
			paths := trustTestPaths(t)
			ws := testWorkspace(t, workspace)
			project, err := newTestApplication().resolveProjectContext(
				t.Context(), ws, trustTestConfig(paths), nil,
				func(cwd string) (trust.Choice, error) {
					for _, choice := range trust.Choices(cwd) {
						if choice.Decision == decision && len(choice.Updates) == 0 {
							return choice, nil
						}
					}
					t.Fatal("startup temporary choice missing")
					return trust.Choice{}, nil
				}, testBuiltInTools(t, ws),
			)
			if err != nil {
				t.Fatal(err)
			}
			if project.trust.Decision != decision || !project.trust.Prompted {
				t.Fatalf("startup resolution = %#v", project.trust)
			}
			if loaded := strings.Contains(project.systemPrompt, "temporary project guidance"); loaded != (decision == trust.DecisionTrusted) {
				t.Fatalf("guidance loaded = %v for %v", loaded, decision)
			}
			if _, found, err := trust.NewStore(paths.GlobalTrust).Lookup(ws.PhysicalPath()); err != nil || found {
				t.Fatalf("temporary choice persisted: found = %v, error = %v", found, err)
			}
		})
	}
}

func TestTrustMenuSelectionsPersistWithoutChangingLoadedContext(t *testing.T) {
	t.Parallel()
	workspace := canonicalTestWorkspace(t)
	menu := (&interactiveSession{workspacePath: workspace}).trustMenu()
	for _, option := range menu.Options {
		t.Run(option.Label, func(t *testing.T) {
			store := trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
			runner := &interactiveSession{
				trustStore: store, workspacePath: workspace,
				trustDecision: trust.DecisionUntrusted, systemPrompt: "loaded prompt",
			}
			output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "trust", Arguments: option.Arguments})
			if err != nil {
				t.Fatal(err)
			}
			index, err := strconv.Atoi(option.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			choice := trust.Choices(workspace)[index]
			entry, found, err := store.Lookup(workspace)
			if err != nil || !found || entry.Decision != choice.Decision {
				t.Fatalf("saved choice = %#v, found = %v, error = %v, want %v", entry, found, err, choice.Decision)
			}
			if !strings.Contains(output, "saved") || !strings.Contains(output, "Restart AICE") {
				t.Fatalf("misleading save result: %q", output)
			}
			if runner.trustDecision != trust.DecisionUntrusted || runner.systemPrompt != "loaded prompt" {
				t.Fatal("/trust changed already-loaded context")
			}
		})
	}
}
