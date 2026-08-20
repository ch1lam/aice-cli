package guard

// Config is the guard configuration owned by config.Settings.
type Config struct {
	Enabled              *bool                `json:"enabled,omitempty"`
	ApplyBuiltinDefaults *bool                `json:"applyBuiltinDefaults,omitempty"`
	Policies             []PolicyRule         `json:"policies,omitempty"`
	PermissionGate       PermissionGateConfig `json:"permissionGate,omitempty"`
	PathAccess           PathAccessConfig     `json:"pathAccess,omitempty"`
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
	requireConfirm := true
	mode := PathAccessAsk
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
		PermissionGate: PermissionGateConfig{
			Patterns:            builtinDangerousPatterns,
			RequireConfirmation: &requireConfirm,
		},
		PathAccess: PathAccessConfig{
			Mode: &mode,
		},
	}
}

// ResolveConfig merges built-in defaults with user config, deduplicating by ID
// (last write wins) so project/global overrides can replace a built-in rule.
func ResolveConfig(user Config) Config {
	def := DefaultConfig()
	if !user.ShouldApplyBuiltins() {
		if user.PermissionGate.Patterns == nil && user.PermissionGate.CustomPatterns == nil {
			// When builtins are disabled and no custom patterns provided, do not inject defaults.
			if user.Enabled == nil {
				user.Enabled = def.Enabled
			}
			if user.ApplyBuiltinDefaults == nil {
				user.ApplyBuiltinDefaults = def.ApplyBuiltinDefaults
			}
			if user.PathAccess.Mode == nil {
				user.PathAccess.Mode = def.PathAccess.Mode
			}
			if user.PermissionGate.RequireConfirmation == nil {
				user.PermissionGate.RequireConfirmation = def.PermissionGate.RequireConfirmation
			}
		}
			return user
	}
	// Merge policies by ID.
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
		_ = r
	}
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
	// PermissionGate: customPatterns replaces builtins; otherwise merge additional patterns.
	if len(user.PermissionGate.CustomPatterns) > 0 {
		out.PermissionGate.Patterns = user.PermissionGate.CustomPatterns
	} else if len(user.PermissionGate.Patterns) > 0 {
		// User supplied extra patterns: append to builtins
		out.PermissionGate.Patterns = append(append([]PatternConfig{}, def.PermissionGate.Patterns...), user.PermissionGate.Patterns...)
	} else {
		out.PermissionGate.Patterns = def.PermissionGate.Patterns
	}
	if out.PermissionGate.RequireConfirmation == nil {
		out.PermissionGate.RequireConfirmation = def.PermissionGate.RequireConfirmation
	}
	if out.PermissionGate.AllowedPatterns == nil {
		out.PermissionGate.AllowedPatterns = def.PermissionGate.AllowedPatterns
	}
	if out.PermissionGate.AutoDenyPatterns == nil {
		out.PermissionGate.AutoDenyPatterns = def.PermissionGate.AutoDenyPatterns
	}
	if out.PathAccess.Mode == nil {
		out.PathAccess.Mode = def.PathAccess.Mode
	}
	if out.PathAccess.AllowedPaths == nil {
		out.PathAccess.AllowedPaths = def.PathAccess.AllowedPaths
	}
	return out
}
