package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveClosesCurrentWebBackendOnEveryExit(t *testing.T) {
	for _, outcome := range []string{"session failure", "model failure", "normal exit after replacement", "TUI failure after replacement"} {
		t.Run(outcome, func(t *testing.T) {
			paths := authTestPaths(t)
			writeConfigFixture(t, paths.GlobalSettings, `{"web":{"search":{"enabled":true,"priority":["service:exa-main"]},"services":{"exa-main":{"provider":"exa","credential":{"auth_ref":"web_services.exa-main"}}}}}`)
			if err := config.SaveDeepSeekAPIKeyFile(paths, "synthetic-model-key"); err != nil {
				t.Fatal(err)
			}
			if err := config.SaveWebCredentialFile(t.Context(), paths, "exa-main", "synthetic-search-key"); err != nil {
				t.Fatal(err)
			}
			search := &fakeWireBackend{}
			backends := testWebBackends(search, &fakeFetch{})
			var closes []int
			backends.search["exa"] = func(config.WebService, config.WebConfig) (webSearchService, error) {
				index := len(closes)
				closes = append(closes, 0)
				return webSearchService{backend: search, origin: "https://fake.example", label: "Fixture", close: func() { closes[index]++ }}, nil
			}
			failure := errors.New("synthetic lifecycle failure")
			tuiCalls := 0
			command, err := newTestCommand(t, dependencies{
				loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
				newModel: func(config.Config) (llm.Streamer, error) {
					if outcome == "model failure" {
						return nil, failure
					}
					return &recordingModel{}, nil
				},
				webBackends: backends,
				userHomeDir: func() (string, error) { return "", nil },
				runTUI: func(ctx context.Context, runner interaction.Runner, _ tui.Options) error {
					tuiCalls++
					if len(closes) != 1 || closes[0] != 0 {
						t.Fatalf("startup backend lifetime = %v", closes)
					}
					if _, err := runner.(interaction.CommandRunner).RunSlashCommand(ctx, interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil {
						return err
					}
					if len(closes) != 2 || closes[0] != 1 || closes[1] != 0 {
						t.Fatalf("publication did not close only the old backend: %v", closes)
					}
					if outcome == "TUI failure after replacement" {
						return failure
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--workspace", t.TempDir(), "--no-approve", "--no-dep-install", "--no-update-check"}
			if outcome == "session failure" {
				path := filepath.Join(t.TempDir(), "broken.jsonl")
				if err := os.WriteFile(path, []byte("broken\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--session", path)
			}
			command.SetIn(strings.NewReader(""))
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(args)
			err = command.ExecuteContext(t.Context())
			switch outcome {
			case "session failure":
				if err == nil || !strings.Contains(err.Error(), "session") {
					t.Fatalf("session preparation error = %v", err)
				}
			case "model failure", "TUI failure after replacement":
				if !errors.Is(err, failure) {
					t.Fatalf("lifecycle failure lost: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			wantBackends, wantTUICalls := 1, 0
			if strings.Contains(outcome, "replacement") {
				wantBackends, wantTUICalls = 2, 1
			}
			if len(closes) != wantBackends || tuiCalls != wantTUICalls {
				t.Fatalf("constructed backends=%d, TUI calls=%d", len(closes), tuiCalls)
			}
			for i, count := range closes {
				if count != 1 {
					t.Errorf("backend %d closed %d times, want exactly once", i, count)
				}
			}
			if search.calls.Load() != 0 {
				t.Fatal("lifecycle test dispatched a search request")
			}
		})
	}
}
