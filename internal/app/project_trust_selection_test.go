package app

import (
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
				request := interaction.CommandRequest{Name: "trust", Arguments: test.input}
				var output string
				var err error
				if entry == "slash" {
					output, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 4, request)
					output, err = result.Output, actionErr
					// Existing Settings actions invalidate drafts even on failure;
					// their result does not claim live application or a commit.
					if result.Revision != 5 || result.Committed || result.Applied || len(result.Warnings) != 0 {
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
				wantRevision := uint64(5)
				if entry == "slash" && err != nil {
					wantRevision = 4
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
