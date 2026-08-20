package guard

// Config is the guard configuration owned by config.Settings.
// For PR1 only file policies are evaluated; other fields are reserved for
// permission-gate and path-access in later PRs.
type Config struct {
	Enabled              *bool       `json:"enabled,omitempty"`
	ApplyBuiltinDefaults *bool       `json:"applyBuiltinDefaults,omitempty"`
	Policies             []PolicyRule `json:"policies,omitempty"`
}

// EnabledOrDefault reports whether guard is enabled.
func (c Config) EnabledOrDefault() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// ShouldApplyBuiltins reports whether built-in defaults should be included.
func (c Config) ShouldApplyBuiltins() bool {
	if c.ApplyBuiltinDefaults == nil {
		return true
	}
	return *c.ApplyBuiltinDefaults
}

// DefaultConfig returns the built-in policy set mirroring pi-guardrails defaults.
func DefaultConfig() Config {
	enabled := true
	apply := true
	return Config{
		Enabled:              &enabled,
		ApplyBuiltinDefaults: &apply,
		Policies: []PolicyRule{
			{
				ID:          "secret-files",
				Description: "Files containing secrets",
				Patterns: []PatternConfig{
					{Pattern: ".env"},
					{Pattern: ".env.local"},
					{Pattern: ".env.production"},
					{Pattern: ".env.prod"},
					{Pattern: ".dev.vars"},
				},
				AllowedPatterns: []PatternConfig{
					{Pattern: "*.example.env"},
					{Pattern: "*.sample.env"},
					{Pattern: "*.test.env"},
					{Pattern: ".env.example"},
					{Pattern: ".env.sample"},
					{Pattern: ".env.test"},
				},
				Protection:   ProtectionNoAccess,
				BlockMessage: "Accessing {file} is not allowed. This file contains secrets. Explain to the user why you want to access this file, and if changes are needed ask the user to make them.",
			},
		},
	}
}

// ResolveConfig merges built-in defaults with user config, deduplicating by ID
// (last write wins) so project/global overrides can replace a built-in rule.
func ResolveConfig(user Config) Config {
	def := DefaultConfig()
	if !user.ShouldApplyBuiltins() {
		// No builtins: only user rules apply (user.Enabled still matters to caller).
		return user
	}
	// Merge: builtins then user rules by ID.
	byID := make(map[string]PolicyRule, len(def.Policies)+len(user.Policies))
	for _, r := range def.Policies {
		byID[r.ID] = r
	}
	for _, r := range user.Policies {
		if r.ID == "" {
			continue
		}
		byID[r.ID] = r
	}
	merged := make([]PolicyRule, 0, len(byID))
	for _, r := range byID {
		// Preserve built-in order first, then user-only IDs in insertion order.
		// For determinism we collect in def order then append new IDs.
		_ = r
	}
	// Stable output: builtins in definition order, then user-only in order.
	seen := make(map[string]bool)
	for _, r := range def.Policies {
		if nr, ok := byID[r.ID]; ok {
			merged = append(merged, nr)
			seen[r.ID] = true
		}
	}
	for _, r := range user.Policies {
		if r.ID == "" || seen[r.ID] {
			continue
		}
		merged = append(merged, r)
		seen[r.ID] = true
	}
	out := user
	out.Policies = merged
	if out.Enabled == nil {
		out.Enabled = def.Enabled
	}
	if out.ApplyBuiltinDefaults == nil {
		out.ApplyBuiltinDefaults = def.ApplyBuiltinDefaults
	}
	return out
}
