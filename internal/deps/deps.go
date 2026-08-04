// Package deps keeps the external executables AICE relies on (ripgrep for the
// grep tool, Git Bash for the bash tool on Windows) available on the host. It
// checks for each helper on PATH and in the AICE bin directory, downloads and
// verifies missing ones, and augments PATH for the current process.
package deps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// noInstallEnv skips downloading missing dependencies when set to a non-empty
// value. Intended for CI and for users who prefer to manage helpers by hand.
const noInstallEnv = "AICE_NO_DEP_INSTALL"

// Options controls dependency resolution. Every field may be left zero to use
// the process default, which keeps Ensure testable with a fake client and
// fake environment hooks.
type Options struct {
	BinDir   string                       // directory where helpers are installed, default ~/.aice/bin
	Goos     string                       // default runtime.GOOS
	Goarch   string                       // default runtime.GOARCH
	BaseURL  string                       // upstream host for downloads, default https://github.com
	LookPath func(string) (string, error) // default exec.LookPath
	Getenv   func(string) string          // default os.Getenv
	Setenv   func(string, string) error   // default os.Setenv
	Client   *http.Client
	Log      io.Writer
}

// DefaultOptions returns Options wired to the current process.
func DefaultOptions() Options {
	return Options{
		Goos:     runtime.GOOS,
		Goarch:   runtime.GOARCH,
		LookPath: exec.LookPath,
		Getenv:   os.Getenv,
		Setenv:   os.Setenv,
		Client:   &http.Client{},
		Log:      io.Discard,
	}
}

// WithBinDir returns a copy of the options with the helper bin directory set.
func (o Options) WithBinDir(dir string) Options {
	o.BinDir = dir
	return o
}

// Ensure makes the required helper executables available. It looks for each
// helper in the bin directory and on PATH, downloads and installs the missing
// ones, then augments PATH so later exec.LookPath calls find them. A failure
// is logged and returned, never fatal: callers keep running and their tools
// degrade gracefully.
func Ensure(ctx context.Context, opts Options) error {
	opts = normalize(opts)
	skip := opts.Getenv(noInstallEnv) != ""
	if skip {
		fmt.Fprintln(opts.Log, "aice: skipping dependency install (AICE_NO_DEP_INSTALL set)")
	}

	// Directories that may hold helpers, in lookup order. On Windows the Git
	// Bash extraction also exposes bin/bash.exe and cmd/git.exe.
	dirs := []string{opts.BinDir}
	if opts.Goos == "windows" {
		gitDir := filepath.Join(opts.BinDir, "git")
		dirs = append(dirs, filepath.Join(gitDir, "bin"), filepath.Join(gitDir, "cmd"))
	}

	var errs []error
	if _, err := lookupIn(opts.Goos, opts.LookPath, dirs, "rg"); err != nil {
		if skip {
			// The user opted out of installs; leave rg missing and degrade.
		} else if err := installRipgrep(ctx, opts); err != nil {
			errs = append(errs, fmt.Errorf("install ripgrep: %w", err))
			fmt.Fprintf(opts.Log, "aice: ripgrep unavailable; install it manually: %s\n",
				ripgrepGuidance(opts.Goos))
		} else {
			fmt.Fprintf(opts.Log, "aice: installed ripgrep %s into %s\n", ripgrepVersion, opts.BinDir)
		}
	}

	if opts.Goos == "windows" {
		if _, err := lookupIn(opts.Goos, opts.LookPath, dirs, "bash"); err != nil {
			if skip {
				// Leave bash missing and degrade.
			} else if err := installGitBash(ctx, opts); err != nil {
				errs = append(errs, fmt.Errorf("install Git Bash: %w", err))
				fmt.Fprintln(opts.Log,
					"aice: Git Bash unavailable; install it manually: winget install Git.Git")
			} else {
				fmt.Fprintf(opts.Log, "aice: installed Git Bash %s into %s\n",
					gitForWindowsTag, filepath.Join(opts.BinDir, "git"))
			}
		}
	}

	if err := opts.Setenv("PATH", prependPath(dirs, opts.Getenv("PATH"))); err != nil {
		errs = append(errs, fmt.Errorf("augment PATH: %w", err))
	}
	return errors.Join(errs...)
}

func normalize(opts Options) Options {
	if opts.Goos == "" {
		opts.Goos = runtime.GOOS
	}
	if opts.Goarch == "" {
		opts.Goarch = runtime.GOARCH
	}
	if opts.BinDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.BinDir = filepath.Join(home, ".aice", "bin")
		}
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "https://github.com"
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Setenv == nil {
		opts.Setenv = os.Setenv
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	return opts
}

// lookupIn returns the first executable named name found in dirs (checked as
// <dir>/<name[.exe]>) and then via lookPath.
func lookupIn(goos string, lookPath func(string) (string, error), dirs []string, name string) (string, error) {
	fileName := name
	if goos == "windows" {
		fileName += ".exe"
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, fileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return lookPath(name)
}

// prependPath returns dirs followed by the directories on current, deduplicated
// and in order, joined with the platform path-list separator.
func prependPath(dirs []string, current string) string {
	existing := []string{}
	if current != "" {
		existing = filepath.SplitList(current)
	}
	seen := make(map[string]bool, len(dirs)+len(existing))
	combined := make([]string, 0, len(dirs)+len(existing))
	for _, dir := range append(append([]string{}, dirs...), existing...) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		combined = append(combined, dir)
	}
	return strings.Join(combined, string(os.PathListSeparator))
}

func ripgrepGuidance(goos string) string {
	if goos == "windows" {
		return "winget install BurntSushi.ripgrep.MSVC"
	}
	return "brew install ripgrep"
}
