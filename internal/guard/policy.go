package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Protection describes how strictly a rule is enforced.
type Protection string

const (
	ProtectionNone     Protection = "none"
	ProtectionReadOnly Protection = "readOnly"
	ProtectionNoAccess Protection = "noAccess"
)

// PolicyRule is the user-facing rule definition.
type PolicyRule struct {
	ID              string          `json:"id"`
	Name            string          `json:"name,omitempty"`
	Description     string          `json:"description,omitempty"`
	Patterns        []PatternConfig `json:"patterns"`
	AllowedPatterns []PatternConfig `json:"allowedPatterns,omitempty"`
	Protection      Protection      `json:"protection"`
	OnlyIfExists    *bool           `json:"onlyIfExists,omitempty"`
	BlockMessage    string          `json:"blockMessage,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
}

func (r PolicyRule) isEnabled() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

func (r PolicyRule) wantsExistsCheck() bool {
	if r.OnlyIfExists == nil {
		return true
	}
	return *r.OnlyIfExists
}

var defaultBlockMessages = map[Protection]string{
	ProtectionNoAccess: "Accessing {file} is not allowed. This file is protected. Ask the user if changes are needed.",
	ProtectionReadOnly: "Writing to {file} is not allowed. This file is read-only.",
	ProtectionNone:     "",
}

// blockedTools maps protection to disallowed tool names.
var blockedTools = map[Protection]map[string]bool{
	ProtectionNoAccess: {"read": true, "write": true, "edit": true, "bash": true, "grep": true, "find": true, "ls": true},
	ProtectionReadOnly: {"write": true, "edit": true, "bash": true},
	ProtectionNone:     {},
}

// isBlocked reports whether toolName is blocked by protection.
func isBlocked(protection Protection, toolName string) bool {
	m, ok := blockedTools[protection]
	if !ok {
		return false
	}
	return m[toolName]
}

type compiledPolicy struct {
	id              string
	protection      Protection
	patterns        []compiledPattern
	allowedPatterns []compiledPattern
	onlyIfExists    bool
	blockMessage    string
}

func compilePolicies(rules []PolicyRule) []compiledPolicy {
	var out []compiledPolicy
	for _, r := range rules {
		if !r.isEnabled() {
			continue
		}
		if strings.TrimSpace(r.ID) == "" || len(r.Patterns) == 0 {
			continue
		}
		msg := r.BlockMessage
		if msg == "" {
			msg = defaultBlockMessages[r.Protection]
		}
		out = append(out, compiledPolicy{
			id:              r.ID,
			protection:      r.Protection,
			patterns:        compileFilePatterns(r.Patterns),
			allowedPatterns: compileFilePatterns(r.AllowedPatterns),
			onlyIfExists:    r.wantsExistsCheck(),
			blockMessage:    msg,
		})
	}
	return out
}

// normalizeTarget mirrors pi-guardrails normalizeTarget but simplified for Go:
//   - "~" and "~/..." are kept as-is with normalization (for pattern matching)
//   - absolute or relative paths are resolved against workspace, then expressed
//     relative to workspace when inside it, otherwise as normalized absolute or "~/..."
func normalizeTarget(filePath, workspace string) string {
	if filePath == "~" || strings.HasPrefix(filePath, "~/") {
		return normalizeFilePath(filePath)
	}
	expanded := expandHome(filePath)
	abs := expanded
	if !filepath.IsAbs(expanded) && workspace != "" {
		abs = filepath.Join(workspace, expanded)
	}
	abs = filepath.Clean(abs)
	if workspace != "" {
		rel, err := filepath.Rel(workspace, abs)
		if err == nil && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return normalizeFilePath(rel)
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
		if abs == home || strings.HasPrefix(abs, home+string(filepath.Separator)) {
			rel, _ := filepath.Rel(home, abs)
			return normalizeFilePath(filepath.Join("~", rel))
		}
	}
	return normalizeFilePath(abs)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func fileExists(p, workspace string) bool {
	expanded := expandHome(p)
	abs := expanded
	if !filepath.IsAbs(expanded) && workspace != "" {
		abs = filepath.Join(workspace, expanded)
	}
	_, err := os.Stat(abs)
	return err == nil
}

// checkPolicy evaluates one compiled policy against an Action that has already
// been filtered to the tool-relevant protection.
func checkPolicy(ctx context.Context, pol compiledPolicy, act Action, workspace string, exists func(string, string) bool) (bool, string) {
	if act.Kind != "file" {
		return false, ""
	}
	if err := ctx.Err(); err != nil {
		return false, ""
	}
	path := normalizeTarget(act.Path, workspace)
	matched := false
	for _, pat := range pol.patterns {
		if pat.test(path) {
			matched = true
			break
		}
	}
	if !matched {
		return false, ""
	}
	for _, pat := range pol.allowedPatterns {
		if pat.test(path) {
			return false, ""
		}
	}
	if pol.onlyIfExists && !act.Unresolved {
		if !exists(path, workspace) {
			return false, ""
		}
	}
	if pol.protection == ProtectionNone {
		return false, ""
	}
	reason := strings.ReplaceAll(pol.blockMessage, "{file}", path)
	if reason == "" {
		reason = fmt.Sprintf("Access to %s is blocked by policy %s.", path, pol.id)
	}
	return true, reason
}
