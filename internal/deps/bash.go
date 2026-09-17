package deps

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// FindNativeBash resolves a host Bash without starting it or installing helpers.
// WSL candidates are separate so their mere presence cannot prevent Git Bash
// provisioning. The tool validates WSL before using it as a fallback.
func FindNativeBash(opts Options) (string, error) {
	opts = normalize(opts)
	if opts.Goos != "windows" {
		return opts.LookPath("bash")
	}

	// Prefer managed and installed Git Bash over other shells on PATH.
	dirs := []string{filepath.Join(opts.BinDir, "git", "bin")}
	// Git's default Windows PATH entry is Git/cmd, which contains git.exe
	// but not bash.exe. Also support Git installations outside PATH.
	if git, err := opts.LookPath("git"); err == nil && filepath.IsAbs(git) {
		dirs = append(dirs, filepath.Join(filepath.Dir(filepath.Dir(git)), "bin"))
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if root := opts.Getenv(key); root != "" {
			dirs = append(dirs, filepath.Join(root, "Git", "bin"))
			if key == "LocalAppData" {
				dirs = append(dirs, filepath.Join(root, "Programs", "Git", "bin"))
			}
		}
	}
	dirs = append(dirs, opts.BinDir)
	// Search every PATH entry: the first bash.exe may be the WSL launcher.
	for _, dir := range strings.Split(opts.Getenv("PATH"), ";") {
		dirs = append(dirs, strings.Trim(dir, `"`))
	}
	for _, dir := range dirs {
		// Do not turn relative PATH entries into trusted absolute executables.
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, "bash.exe")
		if isWSLBash(candidate, opts.Getenv) {
			continue
		}
		resolved, err := opts.LookPath(candidate)
		if err == nil && filepath.IsAbs(resolved) && !isWSLBash(resolved, opts.Getenv) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("native Bash not found; install Git for Windows: winget install Git.Git")
}

// WSLBashPaths returns legacy WSL Bash launchers on PATH in search order.
// Existence alone does not establish that a distribution is usable.
func WSLBashPaths(opts Options) []string {
	opts = normalize(opts)
	if opts.Goos != "windows" {
		return nil
	}
	var paths []string
	seen := make(map[string]bool)
	for _, dir := range strings.Split(opts.Getenv("PATH"), ";") {
		dir = strings.Trim(dir, `"`)
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, "bash.exe")
		if !isWSLBash(candidate, opts.Getenv) {
			continue
		}
		resolved, err := opts.LookPath(candidate)
		key := strings.ToLower(resolved)
		if err == nil && filepath.IsAbs(resolved) && !seen[key] {
			seen[key] = true
			paths = append(paths, resolved)
		}
	}
	return paths
}

func isWSLBash(candidate string, getenv func(string) string) bool {
	// Normalize Windows spelling even in host-independent resolver tests.
	normalizePath := func(value string) string {
		return strings.ToLower(path.Clean(strings.ReplaceAll(value, `\`, "/")))
	}
	candidate = normalizePath(candidate)
	for _, root := range []string{getenv("SystemRoot"), getenv("WINDIR"), `C:\Windows`} {
		if root == "" {
			continue
		}
		for _, dir := range []string{"System32", "Sysnative", "SysWOW64"} {
			if candidate == normalizePath(root+"/"+dir+"/bash.exe") {
				return true
			}
		}
	}
	if root := getenv("LocalAppData"); root != "" {
		aliases := normalizePath(root + "/Microsoft/WindowsApps")
		if strings.HasPrefix(candidate, aliases+"/") {
			return true
		}
	}
	return false
}
