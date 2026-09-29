package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestWebActionEffectsKeepOrInvalidateHeldRuns(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, scenario := range []string{"already first", "credential then prepare failure", "credential then save failure", "preference committed cleanup warning"} {
			t.Run(entry+"/"+scenario, func(t *testing.T) {
				type backendRecord struct {
					backend *fakeWireBackend
					secret  string
					closes  int
				}
				var backendsCreated []*backendRecord
				backends := testWebBackends(&fakeWireBackend{}, &fakeFetch{})
				backends.search["exa"] = func(service config.WebService, _ config.WebConfig) (webSearchService, error) {
					record := &backendRecord{backend: &fakeWireBackend{}, secret: service.Secret}
					backendsCreated = append(backendsCreated, record)
					return webSearchService{backend: record.backend, origin: "https://fake.example", label: "Fixture", close: func() { record.closes++ }}, nil
				}
				s := webCommandSession(t, backends, nil)
				var models []*recordingModel
				s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) {
					model := &recordingModel{response: "answer"}
					models = append(models, model)
					return model, nil
				}
				_, ui := newScriptedUI("key", "old-synthetic-key")
				if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "add", Auth: ui}); err != nil {
					t.Fatal(err)
				}
				var err error
				s.model, s.options, err = resolveModelSettings(s.providers, s.configuration)
				if err != nil {
					t.Fatal(err)
				}
				main, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "held main"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := s.conversation.store.Close(); err != nil {
						t.Error(err)
					}
					s.web.closeBackend()
				})
				_, side, err := s.CreateSideThread("held side")
				if err != nil {
					t.Fatal(err)
				}
				before := s.settingsSnapshot()
				oldTool := s.web.searchTool
				failure := errors.New("synthetic failure")
				request := interaction.CommandRequest{Name: "web", Arguments: "credential"}
				_, request.Auth = newScriptedUI("exa-main", "key", "new-synthetic-key")
				wantRevision, wantResources, wantBackends := uint64(2), uint64(1), 2
				switch scenario {
				case "already first":
					request.Arguments = "up"
					_, request.Auth = newScriptedUI("native")
					wantRevision, wantBackends = 1, 1
				case "credential then prepare failure":
					s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return nil, failure }
				case "credential then save failure":
					s.application.dependencies.saveWebSettings = func(context.Context, config.Paths, config.WebPatch) (config.WebSettings, error) {
						return config.WebSettings{}, failure
					}
				case "preference committed cleanup warning":
					request.Arguments, request.Auth = "fetch", nil
					wantResources = 2
					s.application.dependencies.saveWebSettings = func(ctx context.Context, paths config.Paths, patch config.WebPatch) (config.WebSettings, error) {
						saved, err := config.SaveWebSettingsFile(ctx, paths, patch)
						if err != nil {
							return saved, err
						}
						return saved, &config.CommittedError{Warning: failure}
					}
				}
				var text string
				if entry == "slash" {
					text, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 1, request)
					text, err = result.Output+strings.Join(result.Warnings, "\n"), actionErr
					if result.Committed || result.Applied || result.Revision != wantRevision {
						t.Errorf("Settings result = %+v", result)
					}
				}
				if strings.HasPrefix(scenario, "credential then") {
					if !errors.Is(err, failure) || !strings.Contains(err.Error(), "credential saved to") {
						t.Errorf("credential-only result = %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if scenario == "preference committed cleanup warning" && !strings.Contains(text, failure.Error()) {
					t.Error("cleanup warning lost")
				}
				if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantResources || s.lifecycle.changing {
					t.Errorf("revisions/reservation = %d/%d/%v; want %d/%d/false", s.lifecycle.revision, s.lifecycle.resourceRevision, s.lifecycle.changing, wantRevision, wantResources)
				}
				if len(backendsCreated) != wantBackends {
					t.Fatalf("created backends=%d, want %d", len(backendsCreated), wantBackends)
				}
				published := wantResources == 2
				if published {
					if backendsCreated[0].closes != 1 || backendsCreated[1].closes != 0 || s.web.searchTool == oldTool {
						t.Error("publication did not retire old backend and retain replacement")
					}
				} else {
					if backendsCreated[0].closes != 0 || s.web.searchTool != oldTool || s.loop != before.loop || s.systemPrompt != before.systemPrompt {
						t.Error("no publication replaced live resources")
					}
					if len(backendsCreated) == 2 && (backendsCreated[1].closes != 1 || backendsCreated[1].secret != "new-synthetic-key") {
						t.Error("failed candidate did not retain new credential and close once")
					}
				}
				// The retained tool still dispatches to the backend constructed with the old key.
				if _, err := s.web.searchTool.Execute(t.Context(), llm.ToolCall{ID: "probe", Name: "web_search", Arguments: json.RawMessage(`{"query":"fixture"}`)}); err != nil {
					t.Fatal(err)
				}
				active := backendsCreated[0]
				if published {
					active = backendsCreated[1]
				}
				if active.secret != "old-synthetic-key" || active.backend.calls.Load() != 1 {
					t.Error("active backend changed its frozen credential or was not called")
				}
				mainErr, sideErr := main.Run(t.Context()), runSide(t, side, "held side")
				if published {
					if !errors.Is(mainErr, interaction.ErrSettingsStale) || !errors.Is(sideErr, interaction.ErrSettingsStale) || s.SideThreads()[0].Status != interaction.SideThreadReadOnly {
						t.Errorf("published resources accepted held runs: main=%v side=%v", mainErr, sideErr)
					}
				} else if mainErr != nil || sideErr != nil || s.SideThreads()[0].Status != interaction.SideThreadWritable {
					t.Errorf("unpublished change invalidated held runs: main=%v side=%v", mainErr, sideErr)
				}
				calls := 0
				for _, model := range models {
					calls += len(model.requests)
				}
				snapshot, err := s.conversation.store.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if published && (calls != 0 || len(snapshot.Messages) != 0) || !published && (calls != 2 || len(snapshot.Messages) != 2) {
					t.Errorf("model calls=%d durable messages=%d published=%v", calls, len(snapshot.Messages), published)
				}
			})
		}
	}
}

func TestWebActionAdmission(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), nil)
	status := interaction.CommandRequest{Name: "web", Arguments: " status "}
	s.lifecycle.mainRunning = true
	if output, err := s.RunSlashCommand(t.Context(), status); err != nil || !strings.Contains(output, "Web") {
		t.Fatalf("slash status during run = %q %v", output, err)
	}
	if _, err := s.RunSettingsAction(t.Context(), 0, status); !errors.Is(err, interaction.ErrSettingsRunning) {
		t.Fatalf("Settings status during run = %v", err)
	}
	if _, err := s.RunSettingsAction(t.Context(), 1, status); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatalf("stale status priority = %v", err)
	}
	s.lifecycle.mainRunning = false
	if result, err := s.RunSettingsAction(t.Context(), 0, status); err != nil || result.Revision != 0 || result.Committed || result.Applied {
		t.Fatalf("idle status = %+v %v", result, err)
	}
	writes := 0
	s.application.dependencies.saveWebSettings = func(ctx context.Context, paths config.Paths, patch config.WebPatch) (config.WebSettings, error) {
		writes++
		if _, err := s.beginPreparation(); !errors.Is(err, interaction.ErrSettingsBusy) {
			t.Errorf("preparation during action = %v", err)
		}
		for _, entry := range []string{"slash", "settings"} {
			request := interaction.CommandRequest{Name: "web", Arguments: "fetch"}
			var err error
			if entry == "slash" {
				_, err = s.RunSlashCommand(ctx, request)
			} else {
				_, err = s.RunSettingsAction(ctx, 0, request)
			}
			if !errors.Is(err, interaction.ErrSettingsBusy) {
				t.Errorf("%s concurrent mutation = %v", entry, err)
			}
		}
		return config.SaveWebSettingsFile(ctx, paths, patch)
	}
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunSettingsAction(t.Context(), 0, interaction.CommandRequest{Name: "web", Arguments: "fetch"}); !errors.Is(err, interaction.ErrSettingsStale) {
		t.Fatalf("old draft after commit = %v", err)
	}
	if writes != 1 || s.lifecycle.revision != 1 || s.lifecycle.resourceRevision != 1 || s.lifecycle.changing {
		t.Fatal("status or refused operation changed revisions/reservation")
	}
}
