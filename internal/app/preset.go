package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Presets (Computer Use, browser) only install their helper and write an
// ordinary user MCP service. Everything after that is the generic MCP path.

// ensurePresetService adds, replaces or re-enables user:<id> with definition,
// then approves the resulting connection fingerprint: the user has just
// reviewed this exact service. Tool calls still ask through Guard. A
// replacement keeps the user's own fields, such as tool filters and extra env
// entries; the preset owns only the connection fields and its env keys.
func (s *interactiveSession) ensurePresetService(ctx context.Context, id string, definition config.MCPServerSettings, owned []string, result interaction.SettingsActionResult) (interaction.SettingsActionResult, error) {
	key := "user:" + id
	s.stateMu.RLock()
	current, owner := s.configuration, s.mcp
	s.stateMu.RUnlock()
	existing, exists := current.MCP.Servers[key]
	if exists {
		if existing.Source.Kind != "user" {
			return result, fmt.Errorf("app: a project MCP service already uses the %s ID; remove it first", id)
		}
		definition = presetMerge(existing.Settings, definition, owned)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return result, err
	}
	op := interaction.MCPRequest{Action: "add", Key: id, Definition: encoded}
	if exists {
		op = interaction.MCPRequest{Action: "replace", Key: key, Definition: encoded}
		if reflect.DeepEqual(presetComparable(existing.Settings), presetComparable(definition)) {
			op = interaction.MCPRequest{Action: "enable", Key: key}
			if existing.Enabled {
				op.Action = ""
			}
		}
	}
	if op.Action != "" {
		if result, err = s.applyPresetMCP(ctx, current, owner, op, result); err != nil {
			return result, err
		}
		s.stateMu.RLock()
		current, owner = s.configuration, s.mcp
		s.stateMu.RUnlock()
	}
	if current.MCP.ConnectionDecision(key) != config.MCPConnectionAllow {
		approve := interaction.MCPRequest{Action: "approve", Key: key, Fingerprint: current.MCP.Servers[key].Fingerprint}
		if result, err = s.applyPresetMCP(ctx, current, owner, approve, result); err != nil {
			return result, err
		}
	} else if op.Action == "" {
		// Setup may have installed the helper or granted OS access behind an
		// unchanged definition. Drop the cached connection and its failure.
		if err = s.publishMCPConfiguration(current, key); err != nil {
			return result, err
		}
		result.Applied = true
	}
	// Verify the saved service as /mcp connect does, so the result and the
	// service row report the live connection rather than a pending state.
	s.stateMu.RLock()
	current, owner = s.configuration, s.mcp
	s.stateMu.RUnlock()
	_, _, err = executeMCPManagement(ctx, current, owner, interaction.MCPRequest{Action: "connect", Key: key}, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	result.ReadinessKnown, result.Ready = true, err == nil
	if err != nil {
		result.Warnings = append(result.Warnings, "The service was saved, but its connection test failed; see /mcp for details")
	}
	return result, nil
}

// presetMerge overlays the preset's connection fields and env on the user's
// current definition. Env keys in owned that the preset no longer sets, such as
// a cleared browser target, are removed; other user entries are kept.
func presetMerge(existing, preset config.MCPServerSettings, owned []string) config.MCPServerSettings {
	merged := existing
	merged.Name, merged.Transport, merged.Command, merged.Args, merged.Cwd = preset.Name, preset.Transport, preset.Command, preset.Args, preset.Cwd
	merged.URL, merged.Headers, merged.OAuth = "", nil, nil
	merged.Env = make(map[string]config.MCPValueRef)
	for name, ref := range existing.Env {
		if _, set := preset.Env[name]; !set && !slices.Contains(owned, name) {
			merged.Env[name] = ref
		}
	}
	for name, ref := range preset.Env {
		merged.Env[name] = ref
	}
	return merged
}

// presetComparable ignores user preferences such as enablement and tool
// filters when deciding whether the connection definition must be replaced.
func presetComparable(settings config.MCPServerSettings) config.MCPServerSettings {
	return config.MCPServerSettings{Transport: settings.Transport, Command: settings.Command, Args: settings.Args, Cwd: settings.Cwd, Env: settings.Env}
}

func (s *interactiveSession) applyPresetMCP(ctx context.Context, current config.Config, owner *mcpOwner, op interaction.MCPRequest, result interaction.SettingsActionResult) (interaction.SettingsActionResult, error) {
	next, managed, err := executeMCPManagement(ctx, current, owner, op, nil)
	result.Warnings = append(result.Warnings, managed.Warnings...)
	result.Committed = result.Committed || managed.Committed
	if managed.Committed {
		applyErr := s.publishMCPConfiguration(next)
		result.Applied = applyErr == nil
		err = errors.Join(err, applyErr)
	}
	return result, err
}

// presetStatus summarizes a preset's service for its Settings row.
func presetStatus(configuration config.Config, owner *mcpOwner, key string) (status string, tone interaction.DetailTone, configured bool) {
	services := mcpManagementStatus(configuration, owner, key)
	if len(services) == 0 {
		return "not set up", interaction.ToneMuted, false
	}
	status, tone = mcpServiceSummary(services[0])
	return status, tone, true
}
