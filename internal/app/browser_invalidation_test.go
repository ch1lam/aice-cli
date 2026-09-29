package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestBrowserActionsInvalidateHeldRunsOnlyAfterEffects(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, test := range []struct {
			name    string
			changed bool
		}{
			{name: "cancel before connection"},
			{name: "invalid endpoint"},
			{name: "save failure"},
			{name: "tab helper not started"},
			{name: "connected then cancel tab", changed: true},
			{name: "bind dispatched then invalid JSON", changed: true},
			{name: "new tab dispatched then invalid JSON", changed: true},
			{name: "close failed", changed: true},
		} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				browserSession, dir := browserTestSession(t)
				var models []*recordingModel
				h := newSideHarness(t, func() (agent.Model, error) {
					model := &recordingModel{response: "answer"}
					models = append(models, model)
					return model, nil
				})
				s := h.session
				s.browser = browserSession.browser
				s.configuration.Paths = authTestPaths(t)
				t.Cleanup(func() { _ = closeBrowser(context.Background(), s.browser) })
				helper := filepath.Join(dir, "bin", "agent-browser")
				writeHelper := func(script string) {
					t.Helper()
					if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				input := make(chan string, 1)
				request := interaction.CommandRequest{Name: "browser", Arguments: "connect", Auth: &interaction.AuthInteraction{
					Input: input, Notify: func(_ context.Context, prompt interaction.AuthPrompt) error {
						switch test.name {
						case "cancel before connection":
							return context.Canceled
						case "invalid endpoint":
							input <- "invalid port"
						case "connected then cancel tab":
							if prompt.Menu != nil {
								return context.Canceled
							}
							input <- "9222"
						case "tab helper not started":
							// Lookup succeeds, but Start cannot load the interpreter.
							writeHelper("#!/missing-aice-test-interpreter\n")
							input <- "new"
						case "bind dispatched then invalid JSON":
							input <- "ABC123"
						case "new tab dispatched then invalid JSON":
							input <- "new"
						}
						return nil
					},
				}}
				switch test.name {
				case "save failure":
					request.Arguments = "headed"
					s.application.dependencies.saveSettings = func(context.Context, config.Paths, map[config.Setting]string) error {
						return errors.New("synthetic preference failure")
					}
				case "tab helper not started", "bind dispatched then invalid JSON", "new tab dispatched then invalid JSON", "close failed":
					request.Arguments = "tabs"
					if test.name == "close failed" {
						request.Arguments = "close"
					}
					if err := os.WriteFile(filepath.Join(s.browser.RunDir(), s.browser.Name()+".pid"), []byte("1"), 0o600); err != nil {
						t.Fatal(err)
					}
					writeHelper(`#!/bin/sh
case "$*" in
 *'tab list --json'*) printf '%s\n' '{"success":true,"data":{"tabs":[{"targetId":"ABC123","title":"Example"}]}}';;
 *) printf '%s\n' "$*" >> "$AGENT_BROWSER_SOCKET_DIR/effects"; printf '%s\n' 'invalid-json';;
esac
`)
				}
				main, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "held main"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, side, err := s.CreateSideThread("held side")
				if err != nil {
					t.Fatal(err)
				}
				if entry == "slash" {
					_, err = s.RunSlashCommand(t.Context(), request)
				} else {
					_, err = s.RunSettingsAction(t.Context(), 0, request)
				}
				if err == nil {
					t.Fatal("expected action failure")
				}
				if s.lifecycle.changing {
					t.Fatal("action retained settings reservation")
				}
				wantRevision := uint64(0)
				if test.changed {
					wantRevision = 1
				}
				if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantRevision {
					t.Fatalf("revisions=%d/%d, want %d", s.lifecycle.revision, s.lifecycle.resourceRevision, wantRevision)
				}
				mainErr, sideErr := main.Run(t.Context()), runSide(t, side, "held side")
				if test.changed {
					if !errors.Is(mainErr, interaction.ErrSettingsStale) || !errors.Is(sideErr, interaction.ErrSettingsStale) {
						t.Fatalf("changed resources accepted held runs: main=%v side=%v", mainErr, sideErr)
					}
					if s.SideThreads()[0].Status != interaction.SideThreadReadOnly {
						t.Fatal("stale BTW remained writable in its display projection")
					}
				} else if mainErr != nil || sideErr != nil {
					t.Fatalf("no-effect failure invalidated held runs: main=%v side=%v", mainErr, sideErr)
				}
				calls := 0
				for _, model := range models {
					calls += len(model.requests)
				}
				snapshot, err := h.store.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if test.changed && (calls != 0 || len(snapshot.Messages) != 0) || !test.changed && (calls != 2 || len(snapshot.Messages) != 2) {
					t.Fatalf("model calls=%d durable messages=%d changed=%v", calls, len(snapshot.Messages), test.changed)
				}
				if test.name == "bind dispatched then invalid JSON" || test.name == "new tab dispatched then invalid JSON" || test.name == "close failed" {
					if data, err := os.ReadFile(filepath.Join(s.browser.RunDir(), "effects")); err != nil || strings.Count(string(data), "\n") != 1 {
						t.Fatalf("expected exactly one fake helper dispatch, got %q: %v", data, err)
					}
				}
			})
		}
	}
}

func TestTrustSelectionKeepsHeldRuns(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		t.Run(entry, func(t *testing.T) {
			h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{response: "answer"}, nil })
			s := h.session
			s.trustStore = trust.NewStore(filepath.Join(t.TempDir(), "trust.json"))
			s.workspacePath = h.workspace
			main, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "held main"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, side, err := s.CreateSideThread("held side")
			if err != nil {
				t.Fatal(err)
			}
			request := interaction.CommandRequest{Name: "trust", Arguments: "0"}
			if entry == "slash" {
				_, err = s.RunSlashCommand(t.Context(), request)
			} else {
				_, err = s.RunSettingsAction(t.Context(), 0, request)
			}
			if err != nil || s.lifecycle.revision != 1 || s.lifecycle.resourceRevision != 0 {
				t.Fatalf("restart-only selection changed resource lifetime: %v", err)
			}
			if err := main.Run(t.Context()); err != nil {
				t.Fatalf("held main refused after Trust save: %v", err)
			}
			if err := runSide(t, side, "held side"); err != nil {
				t.Fatalf("held BTW refused after Trust save: %v", err)
			}
		})
	}
}
