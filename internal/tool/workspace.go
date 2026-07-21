// Package tool implements AICE's workspace-scoped agent tools.
package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Workspace confines tool file access to one directory tree.
type Workspace struct {
	path       string
	root       *os.Root
	mutationMu sync.Mutex
}

// NewWorkspace opens path as the root used by all file tools.
func NewWorkspace(path string) (*Workspace, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("tool: workspace path is required")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("tool: resolve workspace path: %w", err)
	}
	root, err := os.OpenRoot(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("tool: open workspace root: %w", err)
	}

	return &Workspace{path: absolutePath, root: root}, nil
}

// Path returns the absolute workspace path used for process working directories.
func (w *Workspace) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Close releases the workspace root. Callers must wait for active tools first.
func (w *Workspace) Close() error {
	if w == nil || w.root == nil {
		return nil
	}
	if err := w.root.Close(); err != nil {
		return fmt.Errorf("tool: close workspace root: %w", err)
	}
	return nil
}

func (w *Workspace) resolvePath(input string, allowRoot bool) (string, error) {
	if w == nil || w.root == nil {
		return "", fmt.Errorf("tool: workspace is required")
	}
	if input == "" {
		return "", fmt.Errorf("tool: path is required")
	}
	if strings.IndexByte(input, 0) >= 0 {
		return "", fmt.Errorf("tool: path contains a null byte")
	}

	path := input
	if filepath.IsAbs(path) {
		relativePath, err := filepath.Rel(w.path, filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("tool: resolve path relative to workspace: %w", err)
		}
		path = relativePath
	}

	path = filepath.Clean(path)
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("tool: path %q escapes the workspace", input)
	}
	if path == "." && !allowRoot {
		return "", fmt.Errorf("tool: path must name a file inside the workspace")
	}
	return path, nil
}
