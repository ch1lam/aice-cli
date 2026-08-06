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
	path         string
	physicalPath string
	mutationMu   sync.Mutex
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
	physicalPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("tool: resolve working directory symlinks: %w", err)
	}

	return &Workspace{
		path:         filepath.Clean(absolutePath),
		physicalPath: filepath.Clean(physicalPath),
	}, nil
}

// Path returns the absolute default working directory.
func (w *Workspace) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// PhysicalPath returns the absolute default working directory with symlinks
// resolved, so entering the same directory through different links is treated
// as one location.
func (w *Workspace) PhysicalPath() string {
	if w == nil {
		return ""
	}
	return w.physicalPath
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
	// Resolve file operations from the cached physical path so parent traversal
	// has the same meaning on Windows and Unix when the workspace is a symlink.
	if strings.HasSuffix(w.physicalPath, string(os.PathSeparator)) {
		return w.physicalPath + input, nil
	}
	return w.physicalPath + string(os.PathSeparator) + input, nil
}
