package guard

import (
	"regexp"
	"strings"
)

// PermissionGateConfig mirrors pi-guardrails permissionGate section.
type PermissionGateConfig struct {
	// Patterns are additional dangerous patterns (substring default, regex opt-in).
	Patterns []PatternConfig `json:"patterns,omitempty"`
	// CustomPatterns if non-empty replaces builtin patterns entirely.
	CustomPatterns []PatternConfig `json:"customPatterns,omitempty"`
	// RequireConfirmation when false only warns (PR2 always denies, flag reserved).
	RequireConfirmation *bool `json:"requireConfirmation,omitempty"`
	// AllowedPatterns bypass dangerous checks.
	AllowedPatterns []PatternConfig `json:"allowedPatterns,omitempty"`
	// AutoDenyPatterns always deny without prompt.
	AutoDenyPatterns []PatternConfig `json:"autoDenyPatterns,omitempty"`
}

// compiledCommandPattern is substring or regex against raw command string.
type compiledCommandPattern struct {
	test   func(string) bool
	source PatternConfig
}

func compileCommandPattern(cfg PatternConfig) compiledCommandPattern {
	if cfg.Regex {
		re, err := regexp.Compile(cfg.Pattern)
		if err != nil {
			return compiledCommandPattern{test: func(string) bool { return false }, source: cfg}
		}
		return compiledCommandPattern{test: func(s string) bool { return re.MatchString(s) }, source: cfg}
	}
	return compiledCommandPattern{test: func(s string) bool { return strings.Contains(s, cfg.Pattern) }, source: cfg}
}

func compileCommandPatterns(cfgs []PatternConfig) []compiledCommandPattern {
	out := make([]compiledCommandPattern, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, compileCommandPattern(c))
	}
	return out
}

// builtinDangerousPatterns mirrors pi-guardrails defaults.
var builtinDangerousPatterns = []PatternConfig{
	{Pattern: "rm -rf", Description: "recursive force delete"},
	{Pattern: "sudo", Description: "superuser command"},
	{Pattern: "dd of=", Description: "disk write operation"},
	{Pattern: "mkfs.", Description: "filesystem format"},
	{Pattern: "chmod -R 777", Description: "insecure recursive permissions"},
	{Pattern: "chown -R", Description: "recursive ownership change"},
	{Pattern: "doas", Description: "privileged command execution"},
	{Pattern: "pkexec", Description: "privileged command execution"},
	{Pattern: "shred", Description: "secure file overwrite"},
	{Pattern: "wipefs", Description: "filesystem signature wipe"},
	{Pattern: "blkdiscard", Description: "block device discard"},
	{Pattern: "fdisk", Description: "disk partitioning"},
	{Pattern: "parted", Description: "disk partitioning"},
	{Pattern: "docker run --privileged", Description: "container with privileged mode"},
}

func matchCommandPattern(input string, patterns []compiledCommandPattern) *compiledCommandPattern {
	for i := range patterns {
		if patterns[i].test(input) {
			return &patterns[i]
		}
	}
	return nil
}

func formatCommandBlockReason(p *compiledCommandPattern, fallback string) string {
	if p == nil {
		return fallback
	}
	if p.source.Description != "" {
		return "Dangerous command blocked (" + p.source.Description + "): " + p.source.Pattern
	}
	return "Dangerous command blocked: " + p.source.Pattern
}
