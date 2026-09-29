package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestProjectTrustSelectionEntryPoints(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"slash", "settings"} {
		for _, test := range []struct {
			name, input, wantError string
			decision               trust.Decision
		}{
			{name: "save trust", input: " \t0\n", decision: trust.DecisionTrusted},
			{name: "save deny", input: "3", decision: trust.DecisionUntrusted},
			{name: "temporary", input: "2", wantError: "app: temporary trust choices are available only at startup"},
			{name: "multiple choices", input: "0 1", wantError: "app: usage: /trust <choice>"},
			{name: "out of range", input: "+99", wantError: `app: invalid trust choice "+99"`},
			{name: "save failure", input: "0", wantError: "app: save project trust: trust: decode store"},
			{name: "missing store", input: "0 1", wantError: "app: trust store is required"},
		} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				workspace := canonicalTestWorkspace(t)
				path := filepath.Join(t.TempDir(), "trust.json")
				store := trust.NewStore(path)
				if test.name == "save failure" {
					if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				s := &interactiveSession{
					trustStore: store, workspacePath: workspace,
					trustDecision: trust.DecisionUntrusted, trustSource: trust.SourceOverride,
					systemPrompt: "already loaded",
					// Trust is restart-only and remains editable during a response.
					lifecycle: settingsLifecycle{revision: 4, resourceRevision: 7, mainRunning: true},
				}
				if test.name == "missing store" {
					s.trustStore = nil
				}
				wantRevision := uint64(5)
				if test.wantError != "" {
					wantRevision = 4
				}
				request := interaction.CommandRequest{Name: "trust", Arguments: test.input}
				var output string
				var err error
				if entry == "slash" {
					output, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 4, request)
					output, err = result.Output, actionErr
					// Trust results do not claim a live preference publication.
					if result.Revision != wantRevision || result.Committed || result.Applied || len(result.Warnings) != 0 {
						t.Fatalf("Settings result = %+v", result)
					}
				}
				if test.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantError) || output != "" {
						t.Fatalf("output=%q error=%v, want %q", output, err, test.wantError)
					}
				} else if err != nil || output != "Trust decision saved. Restart AICE for the new trust state to affect project configuration, prompts, and Skills." {
					t.Fatalf("output=%q error=%v", output, err)
				}
				if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != 7 || s.lifecycle.changing || !s.lifecycle.mainRunning {
					t.Fatal("selection changed reservation or resource revision semantics")
				}
				if s.trustDecision != trust.DecisionUntrusted || s.trustSource != trust.SourceOverride || s.systemPrompt != "already loaded" {
					t.Fatal("selection changed loaded project context")
				}
				if test.name == "save failure" {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "broken" {
						t.Fatal("failed save changed damaged store")
					}
					return
				}
				stored, found, err := store.Lookup(workspace)
				if err != nil || found != (test.wantError == "") || found && stored.Decision != test.decision {
					t.Fatalf("stored=%+v found=%v error=%v", stored, found, err)
				}
			})
		}
	}
}

func TestProjectTrustFailedSaveKeepsDraftForRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{trustStore: trust.NewStore(path), workspacePath: canonicalTestWorkspace(t)}
	request := interaction.CommandRequest{Name: "trust", Arguments: "0"}
	if _, err := s.RunSettingsAction(t.Context(), 0, request); err == nil || !strings.Contains(err.Error(), "trust: decode store") {
		t.Fatalf("first save = %v, want damaged store failure", err)
	}
	// Repair only the test-owned store, then reuse the caller's unchanged draft.
	if err := os.WriteFile(path, []byte(`{"version":1,"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunSettingsAction(t.Context(), 0, request)
	if err != nil || result.Revision != 1 || result.Output != savedProjectTrustMessage {
		t.Fatalf("retry = %+v %v, want one successful save", result, err)
	}
	entry, found, err := s.trustStore.Lookup(s.workspacePath)
	if err != nil || !found || entry.Decision != trust.DecisionTrusted {
		t.Fatalf("retry did not save trust: %v", err)
	}
	request.Arguments = "3"
	if _, err := s.RunSettingsAction(t.Context(), 0, request); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatalf("old draft after successful save = %v", err)
	}
	entry, found, err = s.trustStore.Lookup(s.workspacePath)
	if err != nil || !found || entry.Decision != trust.DecisionTrusted || s.lifecycle.revision != 1 || s.lifecycle.resourceRevision != 0 || s.lifecycle.changing {
		t.Fatal("stale retry changed the saved decision or resource lifetime")
	}
}
