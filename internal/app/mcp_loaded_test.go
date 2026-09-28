package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

type loadedToolsModel struct {
	base    *mcpStartupModel
	session *interactiveSession
	cancel  context.CancelFunc
	fail    bool
}

func (m *loadedToolsModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	want := 0
	for _, d := range request.Tools {
		if strings.HasPrefix(d.Name, "mcp_read_") {
			want++
		}
	}
	revision, _ := m.session.settingsStatus()
	status, err := m.session.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "status user:service0"})
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Loaded tools: %d (latest model request in current Run)", want)
	if !strings.Contains(status.Output, text) {
		return nil, fmt.Errorf("status disagrees with model request: %s", status.Output)
	}
	snapshot, err := m.session.ReadSettings(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, field := range snapshot.Fields {
		if field.ID == "mcp.status" {
			found = strings.Contains(field.Description, text)
		}
	}
	if !found {
		return nil, errors.New("Settings omitted actual loaded tools")
	}
	if m.cancel != nil && want > 0 {
		m.cancel()
		return nil, ctx.Err()
	}
	if m.fail && want > 0 {
		return nil, fmt.Errorf("injected non-retryable failure: %w", agent.ErrContextLimit)
	}
	return m.base.Stream(ctx, request)
}

func TestMCPLoadedToolsActualCommandLifecycle(t *testing.T) {
	endpoint, initialized, calls, _ := mcpStartupServer(t)
	c := ownerTestConfig(t, 0)
	c.DeepSeekAPIKey = "offline-fixture"
	var err error
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: endpoint}}})
	if err != nil {
		t.Fatal(err)
	}
	model := &loadedToolsModel{base: &mcpStartupModel{t: t, search: true, wantRemote: true}}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil },
		newModel:   func(config.Config) (llm.Streamer, error) { return model, nil },
		runTUI: func(ctx context.Context, runner interaction.Runner, _ tui.Options) error {
			s := runner.(*interactiveSession)
			model.session = s
			for _, outcome := range []string{"complete", "cancelled", "failed", "complete"} {
				model.base.round = 0
				runCtx, cancel := context.WithCancel(ctx)
				model.cancel = nil
				model.fail = outcome == "failed"
				if outcome == "cancelled" {
					model.cancel = cancel
				}
				run, err := runner.NewRun(runCtx, interaction.RunInput{Prompt: "find fixture"}, nil)
				if err != nil {
					cancel()
					return err
				}
				err = run.Run(runCtx)
				cancel()
				if (outcome == "cancelled") != errors.Is(err, context.Canceled) || (outcome == "failed") != errors.Is(err, agent.ErrContextLimit) || outcome == "complete" && err != nil {
					return fmt.Errorf("Run outcome: %w", err)
				}
				revision, _ := s.settingsStatus()
				status, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "status user:service0"})
				if err != nil {
					return err
				}
				if !strings.Contains(status.Output, "Loaded tools: 0 (no active main Run)") {
					return errors.New("completed/cancelled Run retained loaded tools")
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", t.TempDir(), "--yolo"})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if initialized.Load() != 1 || calls.Load() != 2 {
		t.Fatal("status caused discovery/calls or changed connection reuse", initialized.Load(), calls.Load())
	}
}

func TestMCPLoadedToolsSnapshotIsolation(t *testing.T) {
	s := &interactiveSession{}
	catalog := &mcpCatalog{entries: map[string]mcpCatalogEntry{
		"ordinary/tool/a":        {service: "ordinary"},
		"ordinary/resource/read": {service: "ordinary"},
		"managed/tool/b":         {service: managedCUAKey},
	}}
	observe, clear := s.beginMCPLoadedTools(catalog)
	observe([]agent.ToolReference{{ID: "ordinary/tool/a"}, {ID: "ordinary/resource/read"}, {ID: "managed/tool/b"}})
	services := []interaction.MCPService{{Key: "ordinary"}, {Key: managedCUAKey}}
	s.applyMCPLoadedTools(services)
	if services[0].LoadedTools != 2 || services[1].LoadedTools != 1 || !services[0].RunActive {
		t.Fatal("service/resource/managed counts mixed", services)
	}
	// Reading concurrently never mutates the Loop/caller's snapshot. Old Run
	// callbacks and cleanup cannot change a later Run's display.
	newObserve, newClear := s.beginMCPLoadedTools(catalog)
	defer newClear()
	newObserve([]agent.ToolReference{{ID: "managed/tool/b"}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				observe([]agent.ToolReference{{ID: "ordinary/tool/a"}})
				clear()
				copy := []interaction.MCPService{{Key: "ordinary"}, {Key: managedCUAKey}}
				s.applyMCPLoadedTools(copy)
				if copy[0].LoadedTools != 0 || copy[1].LoadedTools != 1 || !copy[1].RunActive {
					t.Error("late Run callback replaced current snapshot")
				}
			}
		})
	}
	wg.Wait()
	newClear()
	s.applyMCPLoadedTools(services)
	if services[0].LoadedTools != 0 || services[1].LoadedTools != 0 || services[1].RunActive {
		t.Fatal("Run cleanup retained status")
	}
}
