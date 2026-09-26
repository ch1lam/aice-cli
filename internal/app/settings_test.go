package app

import (
	"context"
	"errors"
	"github.com/ch1lam/aice-cli/internal/web"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestSettingsReadDoesNotCreateSessionOrExposeKeys(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"example"}`)
	writeConfigFixture(t, paths.GlobalAuth, `{"anthropic_api_key":"private-credential"}`)
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	model, options, err := resolveModelSettings(defaultProviders(), c)
	if err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{configuration: c, model: model, options: options, providers: defaultProviders()}
	snapshot, err := s.ReadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Categories) != 5 || s.conversation.store != nil {
		t.Fatal("read created session or omitted categories")
	}
	providers := map[string]bool{}
	for _, field := range snapshot.Fields {
		if strings.Contains(field.Description, "private-credential") || strings.Contains(field.Value.Text, "private-credential") {
			t.Fatal("secret exposed")
		}
		if field.ID == "provider" {
			for _, choice := range field.Choices {
				providers[choice.Value] = true
			}
		}
	}
	if !providers["anthropic"] || !providers["anthropic-subscription"] {
		t.Fatal("current provider catalogs missing")
	}
}

func TestSettingsHeldMainAndSideRunnerRejectChangedRevision(t *testing.T) {
	t.Parallel()
	h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{response: "answer"}, nil })
	main, err := h.session.NewRun(t.Context(), interaction.RunInput{Prompt: "draft"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, side, err := h.session.CreateSideThread("side")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "thinking", Arguments: "low"}); err != nil {
		t.Fatal(err)
	}
	if err := main.Run(t.Context()); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatalf("held main=%v", err)
	}
	if err := runSide(t, side, "draft"); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatalf("held side=%v", err)
	}
	snapshot, err := h.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 0 {
		t.Fatal("stale runner accepted prompt")
	}
}

func TestSettingsSaveBlocksPreparationAndRejectsConcurrentWriter(t *testing.T) {
	t.Parallel()
	h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{response: "answer"}, nil })
	entered, release := make(chan struct{}), make(chan struct{})
	h.application.dependencies.saveSettings = func(context.Context, config.Paths, map[config.Setting]string) error {
		close(entered)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.session.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "thinking", Arguments: "low"})
		done <- err
	}()
	<-entered
	if _, err := h.session.NewRun(t.Context(), interaction.RunInput{Prompt: "draft"}, nil); !errors.Is(err, interaction.ErrSettingsBusy) {
		t.Errorf("NewRun=%v", err)
	}
	if _, _, err := h.session.CreateSideThread("draft"); !errors.Is(err, interaction.ErrSettingsBusy) {
		t.Errorf("CreateSideThread=%v", err)
	}
	if _, err := h.session.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "thinking", Arguments: "high"}); !errors.Is(err, interaction.ErrSettingsBusy) {
		t.Errorf("second edit=%v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSettingsNumericCandidateAndFailureDoNotPublish(t *testing.T) {
	t.Parallel()
	h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{response: "answer"}, nil })
	h.session.configuration.Paths = authTestPaths(t)
	s := h.session
	request := interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "max_turns", Value: interaction.SettingValue{Kind: interaction.SettingInt, Int: 2}}}}
	h.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return nil, errors.New("prepare failed") }
	result, err := s.ApplySettings(t.Context(), request)
	if err == nil || result.Committed || s.configuration.MaxTurns != 0 {
		t.Fatal("preparation failure published")
	}
	if _, err := os.Stat(s.configuration.Paths.GlobalSettings); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation failure saved")
	}
	h.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil }
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"broken":`)
	result, err = s.ApplySettings(t.Context(), request)
	if err == nil || result.Committed || s.configuration.MaxTurns != 0 {
		t.Fatal("write failure published")
	}
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{}`)
	result, err = s.ApplySettings(t.Context(), request)
	if err != nil || !result.Committed || !result.Applied || s.configuration.MaxTurns != 2 || result.Revision != 1 {
		t.Fatalf("save=%+v err=%v", result, err)
	}
	if _, err := s.ApplySettings(t.Context(), request); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatal("stale submit accepted", err)
	}
}

func TestWebSettingsDoNotImportPeerChanges(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), nil)
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"web":{"search":{"timeout":"9s"}}}`)
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if s.configuration.Web.SearchTimeout != config.DefaultWebSearchTimeout {
		t.Fatal("peer timeout imported into live config")
	}
	loaded, err := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
	if err != nil || loaded.Web.SearchTimeout.String() != "9s" || loaded.Web.FetchEnabled {
		t.Fatal("peer update lost on disk", err)
	}
}

func TestSettingsWebPrepareFailureDoesNotSave(t *testing.T) {
	backends := testWebBackends(&fakeWireBackend{}, &fakeFetch{})
	s := webCommandSession(t, backends, nil)
	before := s.settingsSnapshot().configuration.Web.FetchTimeout
	s.application.dependencies.webBackends.newFetcher = func(config.WebConfig) (web.FetchBackend, error) { return nil, errors.New("fake backend failed") }
	result, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "web.fetch.timeout", Value: interaction.SettingValue{Kind: interaction.SettingDuration, Duration: 9 * time.Second}}}})
	if err == nil || result.Committed || s.configuration.Web.FetchTimeout != before {
		t.Fatalf("unexpected save: %+v %v", result, err)
	}
}
