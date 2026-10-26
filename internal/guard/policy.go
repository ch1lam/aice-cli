package guard

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/hostpath"
)

const secretFilesRuleID = "secret-files"

// secretFileNames hold credentials; no tool may read or write them unless the
// name is a template variant in secretFileTemplates. Both match basenames.
var (
	secretFileNames     = []string{".env", ".env.local", ".env.production", ".env.prod", ".dev.vars"}
	secretFileTemplates = []string{"*.example.env", "*.sample.env", "*.test.env", ".env.example", ".env.sample", ".env.test"}
)

// secretFileTools are the tools that can observe or change file contents.
var secretFileTools = map[string]bool{
	"read": true, "write": true, "edit": true, "bash": true, "grep": true, "find": true, "ls": true,
}

// checkSecretFile reports the denial reason when act targets an existing
// secret file. Unresolved bash targets are denied without an existence probe.
func checkSecretFile(ctx context.Context, act Action, workspace string) (string, bool) {
	if act.Kind != "file" || ctx.Err() != nil {
		return "", false
	}
	target := normalizeTarget(act.Path, workspace)
	base := path.Base(target)
	if !matchesAny(secretFileNames, base) || matchesAny(secretFileTemplates, base) {
		return "", false
	}
	// The normalized spelling may start with a literal ~ directory inside the
	// workspace. Probe the action, not that spelling.
	if !act.Unresolved && !fileExists(act.Path, workspace) {
		return "", false
	}
	return secretDenial(target), true
}

func secretDenial(target string) string {
	return "Accessing " + target + " is not allowed. This file contains secrets. Explain to the user why you want to access this file, and if changes are needed ask the user to make them."
}

const credentialsRuleID = "credentials"

// credentialDirs and credentialFiles are home-relative stores of host
// credentials. Every operation on them is denied, whether or not they exist.
var (
	credentialDirs  = []string{".ssh", ".aws", ".gnupg", "Library/Keychains"}
	credentialFiles = []string{
		".netrc", ".git-credentials", ".npmrc", ".pypirc", ".docker/config.json",
		".kube/config", ".config/gh/hosts.yml",
	}
	// sshPublicFiles are public material directly inside ~/.ssh.
	sshPublicFiles = []string{"*.pub", "known_hosts", "known_hosts.old", "config"}
)

// isCredentialPath reports whether abs is a built-in or configured
// credential location.
func (g *Guard) isCredentialPath(abs string) bool {
	for _, file := range g.credentialFiles {
		if hostpath.Same(file, abs) {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	for _, file := range credentialFiles {
		if hostpath.Same(filepath.Join(home, filepath.FromSlash(file)), abs) {
			return true
		}
	}
	for _, dir := range credentialDirs {
		root := filepath.Join(home, filepath.FromSlash(dir))
		if !hostpath.Within(root, abs) {
			continue
		}
		if dir == ".ssh" && filepath.Dir(abs) == root && matchesAny(sshPublicFiles, filepath.Base(abs)) {
			return false
		}
		return true
	}
	return false
}

// isControlPath reports whether abs is a file that configures AICE itself.
func (g *Guard) isControlPath(abs string) bool {
	for _, file := range g.controlFiles {
		if hostpath.Same(file, abs) {
			return true
		}
	}
	return false
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// normalizeTarget expresses filePath for matching and display: relative to
// workspace when inside it, "~/..." under home, otherwise absolute, always
// with forward slashes.
func normalizeTarget(filePath, workspace string) string {
	if filePath == "~" || strings.HasPrefix(filePath, "~/") || strings.HasPrefix(filePath, `~\`) {
		return normalizeFilePath(filePath)
	}
	expanded := hostpath.ExpandTilde(filePath)
	abs := expanded
	if !filepath.IsAbs(expanded) && workspace != "" {
		abs = filepath.Join(workspace, expanded)
	}
	abs = filepath.Clean(abs)
	if hostpath.Within(workspace, abs) {
		rel, err := filepath.Rel(workspace, abs)
		if err == nil && rel != "" {
			return normalizeFilePath(rel)
		}
	}
	if rel, ok := hostpath.UnderHome(abs); ok {
		if rel == "." {
			return "~"
		}
		return normalizeFilePath("~/" + rel)
	}
	return normalizeFilePath(abs)
}

// normalizeFilePath uses forward slashes, strips leading "./" and collapses
// repeated separators.
func normalizeFilePath(input string) string {
	normalized := strings.ReplaceAll(input, "\\", "/")
	for strings.HasPrefix(normalized, "./") {
		normalized = normalized[2:]
	}
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	return normalized
}

func fileExists(p, workspace string) bool {
	expanded := hostpath.ExpandTilde(p)
	if !filepath.IsAbs(expanded) && workspace != "" {
		expanded = filepath.Join(workspace, expanded)
	}
	_, err := os.Stat(expanded)
	return err == nil
}
