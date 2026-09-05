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
	// RequireConfirmation controls dangerous-command asks. When false, these
	// asks are skipped; auto-deny and file/path checks still apply.
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

// Structural dangerous matchers (mirroring pi-guardrails dangerous.ts)
// Each returns a description when the word list is dangerous.
type structuralMatcher func(words []string) string

func rmMatcher(words []string) string {
	if len(words) == 0 || words[0] != "rm" {
		return ""
	}
	hasRecursive := hasShortFlag(words, "r") || hasShortFlag(words, "R") || hasLongOption(words, "recursive") || hasLongOption(words, "dir")
	hasForce := hasShortFlag(words, "f") || hasLongOption(words, "force")
	if hasRecursive && hasForce {
		return "recursive force delete"
	}
	return ""
}

func shredMatcher(words []string) string {
	if len(words) > 0 && words[0] == "shred" {
		return "secure file overwrite"
	}
	return ""
}
func sudoMatcher(words []string) string {
	if len(words) > 0 && words[0] == "sudo" {
		return "superuser command"
	}
	return ""
}
func doasMatcher(words []string) string {
	if len(words) > 0 && words[0] == "doas" {
		return "privileged command execution"
	}
	return ""
}
func pkexecMatcher(words []string) string {
	if len(words) > 0 && words[0] == "pkexec" {
		return "privileged command execution"
	}
	return ""
}
func ddMatcher(words []string) string {
	if len(words) == 0 || words[0] != "dd" {
		return ""
	}
	if hasArg(words, "of=") {
		return "disk write operation"
	}
	return ""
}
func mkfsMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "mkfs" || strings.HasPrefix(cmd, "mkfs.") {
		return "filesystem format"
	}
	return ""
}
func wipefsMatcher(words []string) string {
	if len(words) > 0 && words[0] == "wipefs" {
		return "filesystem signature wipe"
	}
	return ""
}
func blkdiscardMatcher(words []string) string {
	if len(words) > 0 && words[0] == "blkdiscard" {
		return "block device discard"
	}
	return ""
}
func fdiskMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "fdisk" || cmd == "sfdisk" || cmd == "cfdisk" {
		return "disk partitioning"
	}
	return ""
}
func partedMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "parted" || cmd == "sgdisk" {
		return "disk partitioning"
	}
	return ""
}
func chmodMatcher(words []string) string {
	if len(words) == 0 || words[0] != "chmod" {
		return ""
	}
	hasRecursive := hasShortFlag(words, "R") || hasLongOption(words, "recursive")
	hasWorldWritable := false
	for _, w := range words {
		if w == "777" || w == "0777" || w == "a+rwx" || w == "ugo+rwx" || w == "7777" || w == "1777" {
			hasWorldWritable = true
			break
		}
	}
	if hasRecursive && hasWorldWritable {
		return "insecure recursive permissions"
	}
	return ""
}
func chownMatcher(words []string) string {
	if len(words) == 0 || words[0] != "chown" {
		return ""
	}
	if hasShortFlag(words, "R") || hasLongOption(words, "recursive") {
		return "recursive ownership change"
	}
	return ""
}
func containerMatcher(words []string) string {
	if len(words) < 2 {
		return ""
	}
	cmd := words[0]
	if cmd != "docker" && cmd != "podman" {
		return ""
	}
	sub := words[1]
	if sub != "run" && sub != "create" {
		return ""
	}
	for _, w := range words {
		if w == "--privileged" || strings.HasPrefix(w, "--privileged=") {
			return "container with privileged mode"
		}
	}
	return ""
}

var builtinStructuralMatchers = []structuralMatcher{
	rmMatcher, shredMatcher, sudoMatcher, doasMatcher, pkexecMatcher,
	ddMatcher, mkfsMatcher, wipefsMatcher, blkdiscardMatcher,
	fdiskMatcher, partedMatcher, chmodMatcher, chownMatcher, containerMatcher,
}

// prefixSuppressedBinaries are commands that must not yield a session-grant
// prefix; authorizing them would be as broad as skipping the dangerous check.
var prefixSuppressedBinaries = map[string]bool{
	"rm": true, "sudo": true, "doas": true, "pkexec": true,
	"dd": true, "shred": true, "wipefs": true, "blkdiscard": true,
	"fdisk": true, "sfdisk": true, "cfdisk": true, "parted": true,
	"sgdisk": true, "chmod": true, "chown": true, "mkfs": true,
}

func isPrefixSuppressedBinary(cmd string) bool {
	if prefixSuppressedBinaries[cmd] {
		return true
	}
	return strings.HasPrefix(cmd, "mkfs.")
}

// CommandPrefix returns a suggested session-grant prefix for command.
// It returns "" when no safe prefix exists: compound commands, suppressed
// dangerous binaries, and docker/podman run or create.
func CommandPrefix(command string) string {
	calls := parseCallWords(command)
	if len(calls) != 1 {
		return ""
	}
	words := calls[0]
	if len(words) == 0 {
		return ""
	}
	if isPrefixSuppressedBinary(words[0]) {
		return ""
	}
	if len(words) >= 2 {
		isContainer := words[0] == "docker" || words[0] == "podman"
		isRunOrCreate := words[1] == "run" || words[1] == "create"
		if isContainer && isRunOrCreate {
			return ""
		}
	}
	prefix := words[0]
	if len(words) >= 2 {
		sub := words[1]
		isSubcommand := !strings.HasPrefix(sub, "-") && !strings.Contains(sub, "/") && !strings.Contains(sub, "=")
		if isSubcommand {
			prefix = words[0] + " " + words[1]
		}
	}
	return prefix
}

// commandCoveredByPrefixes reports whether every parsed subcommand is covered
// by a session command-prefix grant. Coverage is exact match or a word-boundary
// prefix (joined words equal the grant, or start with grant plus a space).
func commandCoveredByPrefixes(cmd string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return false
	}
	calls := parseCallWords(cmd)
	if len(calls) == 0 {
		return false
	}
	for _, words := range calls {
		joined := strings.Join(words, " ")
		if !commandMatchesAnyPrefix(joined, prefixes) {
			return false
		}
	}
	return true
}

func commandMatchesAnyPrefix(joined string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if joined == prefix || strings.HasPrefix(joined, prefix+" ") {
			return true
		}
	}
	return false
}

// structuralDangerousMatch checks each parsed command's word list against
// builtin structural matchers. Returns description and matched pattern when hit.
func structuralDangerousMatch(command string) (string, string) {
	calls := parseCallWords(command)
	for _, words := range calls {
		for _, m := range builtinStructuralMatchers {
			if desc := m(words); desc != "" {
				// Use the raw command prefix as pattern for reason formatting
				pat := strings.Join(words, " ")
				if len(pat) > 60 {
					pat = pat[:60] + "..."
				}
				return desc, pat
			}
		}
	}
	return "", ""
}
