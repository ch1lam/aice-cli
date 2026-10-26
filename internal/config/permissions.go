package config

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const permissionsKey = "permissions"

// PermissionRules are explicit tool permission rules. Unlike scalar
// settings, the lists of user settings and trusted project settings are
// concatenated; the execution gate parses and orders them (deny, ask, allow).
type PermissionRules struct {
	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func (r *PermissionRules) merge(other PermissionRules) {
	r.Allow = append(r.Allow, other.Allow...)
	r.Ask = append(r.Ask, other.Ask...)
	r.Deny = append(r.Deny, other.Deny...)
}

func decodePermissionRules(raw any) (PermissionRules, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return PermissionRules{}, err
	}
	var rules PermissionRules
	if err := jsonutil.DecodeStrict(data, &rules); err != nil {
		return PermissionRules{}, fmt.Errorf("config: permissions must be an object of allow, ask and deny rule lists: %w", err)
	}
	for _, list := range [][]string{rules.Allow, rules.Ask, rules.Deny} {
		for _, rule := range list {
			if strings.TrimSpace(rule) == "" {
				return PermissionRules{}, fmt.Errorf("config: permissions contain an empty rule")
			}
		}
	}
	return rules, nil
}

// SavePermissionRule appends rule to the user settings list for decision
// ("allow", "ask" or "deny"). An existing identical rule leaves the file
// unchanged.
func SavePermissionRule(ctx context.Context, paths Paths, decision, rule string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	return editFile(ctx, paths.GlobalSettings, func(values map[string]any) error {
		var rules PermissionRules
		if existing, ok := values[permissionsKey]; ok {
			decoded, err := decodePermissionRules(existing)
			if err != nil {
				return fmt.Errorf("existing permissions left unchanged: %w", err)
			}
			rules = decoded
		}
		var list *[]string
		switch decision {
		case "allow":
			list = &rules.Allow
		case "ask":
			list = &rules.Ask
		case "deny":
			list = &rules.Deny
		default:
			return fmt.Errorf("unknown permission decision %q", decision)
		}
		if slices.Contains(*list, rule) {
			return errConfigUnchanged
		}
		*list = append(*list, rule)
		values[permissionsKey] = rules
		return nil
	})
}
