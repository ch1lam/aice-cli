package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func desktopSettingsSession(t *testing.T) *interactiveSession {
	t.Helper()
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"provider": "deepseek"})
	s.desktop = &desktopState{
		install: func(context.Context, deps.Options) (deps.CuaInstallResult, error) {
			return deps.CuaInstallResult{Installed: true, Installation: deps.CuaInstallation{Binary: "/synthetic/driver"}}, nil
		},
		setup: func(context.Context, string, desktop.SetupOptions) (desktop.SetupResult, error) {
			return desktop.SetupResult{LaunchRequested: true, AuthorizationRequested: true, AuthorizationCompleted: true, CaptureVerified: true, Ready: true}, nil
		},
	}
	s.guardAdapter.desktop = s.desktop
	return s
}

func TestDesktopSettingsSetupUsesOneReservationAndRetainsExternalSuccess(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "save-failed"}[failSave], func(t *testing.T) {
			s := desktopSettingsSession(t)
			native := s.desktop.setup
			s.desktop.setup = func(ctx context.Context, binary string, options desktop.SetupOptions) (desktop.SetupResult, error) {
				if binary != "/synthetic/driver" {
					t.Fatal("unverified binary supplied")
				}
				if _, err := s.ReadSettings(ctx); err != nil {
					t.Fatal("read blocked by long setup", err)
				}
				if _, err := s.NewRun(ctx, interaction.RunInput{Prompt: "must not start"}, nil); !errors.Is(err, interaction.ErrSettingsBusy) {
					t.Fatalf("run started during setup: %v", err)
				}
				if _, err := s.ApplySettings(ctx, interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "max_turns", Value: interaction.SettingValue{Kind: interaction.SettingInt, Int: 2}}}}); !errors.Is(err, interaction.ErrSettingsBusy) {
					t.Fatalf("concurrent writer=%v", err)
				}
				if failSave {
					writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"broken":`)
				} else {
					// No settings file lock is held during native UI. A peer update
					// must survive our later narrow preference commit.
					_, err := config.SaveSettingsPatch(ctx, s.configuration.Paths, config.SettingsPatch{Changes: []config.SettingChange{{ID: "no_update_check", Value: config.SettingValue{Kind: "bool", Bool: true}}}})
					if err != nil {
						t.Fatal(err)
					}
				}
				return native(ctx, binary, options)
			}
			ui, auth := newScriptedUI("continue")
			result, err := s.RunSettingsAction(t.Context(), 0, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
			if (err != nil) != failSave || result.Committed == failSave || result.Applied == failSave {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(result.External) != 3 || !result.External[0].Completed || !result.External[2].Completed || result.Revision != 1 {
				t.Fatalf("partial facts=%+v", result)
			}
			if s.configuration.DesktopEnabled == failSave || s.conversation.store != nil {
				t.Fatal("wrong publication or setup created Session")
			}
			if len(ui.prompts) < 3 || !strings.Contains(ui.prompts[0].Instructions, "outside this project") || !strings.Contains(ui.prompts[0].Instructions, "current model provider") || !strings.Contains(ui.prompts[0].Instructions, "direct capture") {
				t.Fatal("disclosures not presented")
			}
			if failSave {
				if slicesContains(toolNames(s.tools), "desktop_act") || !strings.Contains(result.Output, "External setup steps are retained") {
					t.Fatal("failed save changed tools or lost facts")
				}
			} else {
				loaded, err := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
				if err != nil || !loaded.NoUpdateCheck || s.configuration.NoUpdateCheck {
					t.Fatalf("peer update lost or imported: %v", err)
				}
			}
		})
	}
}

func TestDesktopSettingsDeclineAndPreferenceOnlyDoNotTouchNative(t *testing.T) {
	for _, kind := range []string{"decline", "closed-input", "cancelled", "preference-only", "missing-ui"} {
		t.Run(kind, func(t *testing.T) {
			s := desktopSettingsSession(t)
			s.desktop.install = func(context.Context, deps.Options) (deps.CuaInstallResult, error) {
				t.Fatal("unexpected native installation")
				return deps.CuaInstallResult{}, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, auth := newScriptedUI("cancel")
			action := "setup"
			switch kind {
			case "closed-input":
				input := make(chan string)
				close(input)
				auth.Input = input
			case "cancelled":
				cancel()
			case "preference-only":
				_, auth = newScriptedUI("continue")
				action = "enable"
			case "missing-ui":
				auth = nil
			}
			result, err := s.RunSettingsAction(ctx, 0, interaction.CommandRequest{Name: "desktop", Arguments: action, Auth: auth})
			if result.Committed != (kind == "preference-only") || len(result.External) != 0 || result.ReadinessKnown || s.conversation.store != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if kind == "missing-ui" && err == nil {
				t.Fatal("confirmationless setup accepted")
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestDesktopSettingsRetainsStartupDownloadPolicy(t *testing.T) {
	s := desktopSettingsSession(t)
	s.desktop.installOptions.NoInstall = true
	saved, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "no_dep_install", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: false}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.desktop.install = func(_ context.Context, options deps.Options) (deps.CuaInstallResult, error) {
		if !options.NoInstall {
			t.Fatal("restart-only change authorized download")
		}
		return deps.CuaInstallResult{}, errors.New("download disabled")
	}
	ui, auth := newScriptedUI("continue")
	result, err := s.RunSettingsAction(t.Context(), saved.Revision, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
	if err == nil || result.Committed || !strings.Contains(ui.prompts[0].Instructions, "disabled for this instance") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(s.configuration.Paths.GlobalSettings)
	if err != nil || strings.Contains(string(data), `"desktop_enabled"`) {
		t.Fatalf("setup failure saved enable: %s %v", data, err)
	}
}

func TestDesktopSetupCancellationKeepsInstalledFactWithoutPublishing(t *testing.T) {
	s := desktopSettingsSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.desktop.setup = func(context.Context, string, desktop.SetupOptions) (desktop.SetupResult, error) {
		cancel()
		return desktop.SetupResult{AuthorizationRequested: true}, context.Canceled
	}
	_, auth := newScriptedUI("continue")
	result, err := s.RunSettingsAction(ctx, 0, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
	if !errors.Is(err, context.Canceled) || result.Committed || result.Applied || result.Ready || len(result.External) != 2 || !result.External[0].Completed || result.External[1].Completed || result.Revision != 1 {
		t.Fatalf("cancelled result=%+v err=%v", result, err)
	}
	if s.configuration.DesktopEnabled || slicesContains(toolNames(s.tools), "desktop_act") || s.conversation.store != nil {
		t.Fatal("cancel published or created Session")
	}
	if _, reason := s.settingsStatus(); reason != "" {
		t.Fatal("setup reservation leaked", reason)
	}
}

func TestDesktopSetupIsUnavailableDuringActiveRun(t *testing.T) {
	s := desktopSettingsSession(t)
	s.lifecycle.mainRunning = true
	ui, auth := newScriptedUI("continue")
	_, err := s.RunSettingsAction(t.Context(), 0, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
	if !errors.Is(err, interaction.ErrSettingsRunning) || len(ui.prompts) != 0 {
		t.Fatalf("active setup=%v prompts=%d", err, len(ui.prompts))
	}
}

func TestDesktopSetupWindowSelectionAndCaptureFacts(t *testing.T) {
	for _, answer := range []string{"selected", "cancel", "foreign"} {
		t.Run(answer, func(t *testing.T) {
			s := desktopSettingsSession(t)
			s.desktop.setup = func(ctx context.Context, _ string, options desktop.SetupOptions) (desktop.SetupResult, error) {
				ref, err := options.SelectWindow(ctx, []desktop.Window{{Ref: "selected", App: "Fixture", Title: "Synthetic window", PID: 41, WindowID: 99}})
				return desktop.SetupResult{ConnectionVerified: true, CaptureVerified: ref == "selected" && err == nil, Ready: ref == "selected" && err == nil}, err
			}
			ui, auth := newScriptedUI("continue", answer)
			result, err := s.RunSettingsAction(t.Context(), 0, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
			if (err == nil) != (answer == "selected") || result.Committed != (answer == "selected") || len(result.External) != 3 || result.External[2].Completed != (answer == "selected") {
				t.Fatal(result, err)
			}
			if s.conversation.store != nil || s.desktop.setupCaptureAt.IsZero() != (answer != "selected") {
				t.Fatal("setup created history or invented capture evidence")
			}
			prompt := ui.prompts[len(ui.prompts)-1]
			if prompt.Menu == nil || prompt.Menu.Options[0].Arguments != "cancel" || !strings.Contains(prompt.Instructions, "not sent to a model") {
				t.Fatal("window capture was not explicitly disclosed", prompt)
			}
		})
	}
}
