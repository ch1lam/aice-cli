// Package tool implements AICE's built-in agent tools.
package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Workspace defines the default working directory for agent tools.
type Workspace struct {
	path       string
	mutationMu sync.Mutex
}

// NewWorkspace resolves and validates the default working directory.
func NewWorkspace(path string) (*Workspace, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("tool: workspace path is required")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, fmt.Errorf("tool: workspace path contains a null byte")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("tool: resolve workspace path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("tool: inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("tool: working directory %q is not a directory", path)
	}

	return &Workspace{path: filepath.Clean(absolutePath)}, nil
}

// Path returns the absolute default working directory.
func (w *Workspace) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

func (w *Workspace) resolvePath(input string) (string, error) {
	if w == nil || w.path == "" {
		return "", fmt.Errorf("tool: workspace is required")
	}
	if input == "" {
		return "", fmt.Errorf("tool: path is required")
	}
	if strings.IndexByte(input, 0) >= 0 {
		return "", fmt.Errorf("tool: path contains a null byte")
	}

	if filepath.IsAbs(input) {
		return input, nil
	}
	// Keep relative components for the OS to resolve after symlinks, matching
	// how the same path behaves from an actual process working directory.
	if strings.HasSuffix(w.path, string(os.PathSeparator)) {
		return w.path + input, nil
	}
	return w.path + string(os.PathSeparator) + input, nil
}
