package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestManagedCUAManagementStatusAndSingleSettingOwner(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			paths := trustTestPaths(t)
			writeConfigFixture(t, paths.GlobalSettings, fmt.Sprintf(`{"desktop_enabled":%t}`, enabled))
			before, err := os.ReadFile(paths.GlobalSettings)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"", managedCUAKey} {
				args := []string{"status"}
				if key != "" {
					args = append(args, key)
				}
				result, err := managementCommand(t, paths, "", args...)
				if err != nil || len(result.Services) != 1 {
					t.Fatal("managed status missing", result, err)
				}
				service := result.Services[0]
				if service.Key != managedCUAKey || !service.Managed || service.Enabled != enabled || service.SettingsField != "desktop_enabled" || service.CatalogKnown || service.Fingerprint != "" || service.Definition != nil {
					t.Fatal("managed status invented connection evidence", service)
				}
				if !strings.Contains(formatMCPStatus(result.Services), "/desktop") {
					t.Fatal("missing management route")
				}
			}
			current, err := config.LoadFiles(paths, config.LoadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"replace", "enable", "disable", "remove", "approve", "deny", "forget", "connect", "reconnect", "login", "logout", "permissions"} {
				_, result, err := executeMCPManagement(t.Context(), current, nil, interaction.MCPRequest{Action: action, Key: managedCUAKey}, nil)
				if err == nil || !strings.Contains(err.Error(), "/desktop") || result.Committed {
					t.Fatal("managed entry accepted ordinary action", action, err)
				}
			}
			after, err := os.ReadFile(paths.GlobalSettings)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("management changed Computer Use preference", err)
			}
		})
	}
}

func TestManagedCUAMenuRedirectIsReadOnlyAndAvailableDuringRun(t *testing.T) {
	runMCPSettingsFixture(t, `{"transport":"http","url":"https://example.com/mcp"}`, func(ctx context.Context, s *interactiveSession) {
		before, _ := os.ReadFile(s.configuration.Paths.GlobalSettings)
		revision, _ := s.settingsStatus()
		s.lifecycle.mu.Lock()
		s.lifecycle.mainRunning = true
		s.lifecycle.mu.Unlock()
		defer func() { s.lifecycle.mu.Lock(); s.lifecycle.mainRunning = false; s.lifecycle.mu.Unlock() }()
		result, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "desktop"})
		if err != nil || result.FocusSetting != "desktop_enabled" || result.Committed || result.Revision != revision || s.configuration.DesktopEnabled || s.conversation.store != nil {
			t.Fatal("navigation changed runtime or required idle", result, err)
		}
		after, _ := os.ReadFile(s.configuration.Paths.GlobalSettings)
		if !bytes.Equal(before, after) {
			t.Fatal("navigation wrote preferences")
		}
	})
	configuration := config.Config{DesktopEnabled: true}
	ui := &interaction.AuthInteraction{Input: make(chan string), Notify: func(context.Context, interaction.AuthPrompt) error {
		t.Error("managed entry appeared in an ordinary service picker")
		return context.Canceled
	}}
	if _, err := prepareMCPCommand(t.Context(), configuration, nil, ui, interaction.MCPRequest{Action: "approve"}); err == nil {
		t.Fatal("managed service treated as an editable ordinary connection")
	}
}
