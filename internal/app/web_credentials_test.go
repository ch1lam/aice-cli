package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestWebCredentialCommitBoundary(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, tc := range []struct {
			action                      string
			writeFails, preferenceFails bool
		}{
			{action: "add"}, {action: "credential"}, {action: "remove"},
			{action: "add", preferenceFails: true}, {action: "credential", preferenceFails: true},
			{action: "add", writeFails: true}, {action: "credential", writeFails: true}, {action: "remove", writeFails: true},
		} {
			name := entry + "/" + tc.action
			if tc.writeFails {
				name += "/write failure"
			} else if tc.preferenceFails {
				name += "/committed then preference failure"
			} else {
				name += "/committed cleanup warning"
			}
			t.Run(name, func(t *testing.T) {
				search := &fakeWireBackend{}
				backends := testWebBackends(search, &fakeFetch{})
				backends.search["exa"] = fakeSearchFactory(search, "exa-rest")
				s := webCommandSession(t, backends, nil)
				if tc.action != "add" {
					_, ui := newScriptedUI("key", "old-synthetic-key")
					if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "add", Auth: ui}); err != nil {
						t.Fatal(err)
					}
				}
				before := s.settingsSnapshot()
				revision, _ := s.settingsStatus()
				cleanupErr := errors.New("synthetic auth lock cleanup")
				writeErr := errors.New("synthetic auth write failure")
				preferenceErr := errors.New("synthetic preference failure")
				credentialWrites, preferenceWrites := 0, 0
				s.application.dependencies.saveWebCredential = func(ctx context.Context, paths config.Paths, id, secret string) error {
					credentialWrites++
					if tc.writeFails {
						return writeErr
					}
					// Real replacement first: CommittedError models only the later cleanup failure.
					if err := config.SaveWebCredentialFile(ctx, paths, id, secret); err != nil {
						return err
					}
					return &config.CommittedError{Warning: cleanupErr}
				}
				s.application.dependencies.saveWebSettings = func(ctx context.Context, paths config.Paths, patch config.WebPatch) (config.WebSettings, error) {
					preferenceWrites++
					if tc.preferenceFails {
						return config.WebSettings{}, preferenceErr
					}
					return config.SaveWebSettingsFile(ctx, paths, patch)
				}
				answers := []string{"key", "new-synthetic-key"}
				if tc.action == "credential" {
					answers = append([]string{"exa-main"}, answers...)
				}
				if tc.action == "remove" {
					answers = []string{"exa-main"}
				}
				_, ui := newScriptedUI(answers...)
				request := interaction.CommandRequest{Name: "web", Arguments: tc.action, Auth: ui}
				var output, warnings string
				var err error
				if entry == "slash" {
					output, err = s.RunSlashCommand(t.Context(), request)
					warnings = output
				} else {
					var result interaction.SettingsActionResult
					result, err = s.RunSettingsAction(t.Context(), revision, request)
					output, warnings = result.Output, strings.Join(result.Warnings, "\n")
				}

				wantSecret := "new-synthetic-key"
				if tc.action == "remove" {
					wantSecret = ""
				}
				if tc.writeFails {
					wantSecret = "old-synthetic-key"
					if tc.action == "add" {
						wantSecret = ""
					}
				}
				loaded, loadErr := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				// Resolving a reference observes the private credential cache even after removal.
				for source, c := range map[string]config.Config{"disk": loaded, "memory": s.configuration} {
					probe, probeErr := c.WithWebPatch(config.WebPatch{Services: map[string]*config.WebServiceSettings{
						"exa-main": {Provider: "exa", Credential: config.WebCredentialRef{AuthRef: "web_services.exa-main"}},
					}})
					if probeErr != nil {
						t.Fatal(probeErr)
					}
					if probe.Web.Services["exa-main"].Secret != wantSecret {
						t.Errorf("%s credential does not match the write commit outcome", source)
					}
				}
				wantPreferenceWrites := 1
				if tc.writeFails && tc.action != "remove" {
					wantPreferenceWrites = 0
				}
				if credentialWrites != 1 || preferenceWrites != wantPreferenceWrites {
					t.Errorf("writes: credential=%d preference=%d; want 1/%d", credentialWrites, preferenceWrites, wantPreferenceWrites)
				}
				if strings.Contains(warnings, cleanupErr.Error()) != !tc.writeFails {
					t.Errorf("cleanup warning missing or misclassified: %q; err=%v", warnings, err)
				}
				if strings.Contains(output+warnings, "synthetic-key") {
					t.Fatal("credential leaked in action output")
				}
				switch {
				case tc.preferenceFails:
					if !errors.Is(err, preferenceErr) || !strings.Contains(err.Error(), "credential saved to") {
						t.Errorf("credential-only outcome = %v", err)
					}
				case tc.writeFails && tc.action != "remove":
					if !errors.Is(err, writeErr) {
						t.Errorf("credential failure = %v", err)
					}
				case tc.writeFails:
					if err != nil || !strings.Contains(output, "Stored credential could not be removed") {
						t.Errorf("removal partial success = %q %v", output, err)
					}
				default:
					if err != nil {
						t.Errorf("committed credential treated as failure: %v", err)
					}
					if tc.action == "remove" && !strings.Contains(output, "Stored credential removed") {
						t.Errorf("removal message = %q", output)
					}
				}
				if tc.preferenceFails || tc.writeFails && tc.action != "remove" {
					if !reflect.DeepEqual(s.configuration.Web, before.configuration.Web) || s.loop != before.loop || s.systemPrompt != before.systemPrompt || !reflect.DeepEqual(toolNames(s.tools), toolNames(before.tools)) {
						t.Error("failed preference action published runtime resources")
					}
				} else if s.loop == before.loop || !reflect.DeepEqual(loaded.Web, s.configuration.Web) {
					t.Error("saved preference was not published")
				}
				wantRevision, wantResources := revision, revision
				if !tc.writeFails || tc.action == "remove" {
					wantRevision++
				}
				if !tc.preferenceFails && (!tc.writeFails || tc.action == "remove") {
					wantResources++
				}
				if got, reason := s.settingsStatus(); got != wantRevision || reason != "" || s.lifecycle.resourceRevision != wantResources {
					t.Errorf("revision/resource/reservation = %d/%d %q; want %d/%d", got, s.lifecycle.resourceRevision, reason, wantRevision, wantResources)
				}
				if search.calls.Load() != 0 {
					t.Fatal("credential action called search API")
				}
			})
		}
	}
}
