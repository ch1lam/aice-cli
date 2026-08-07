package trust

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CanonicalPath normalizes path for use as a trust key: it resolves symlinks
// and cleans the result so entering the same directory through different links
// shares one decision.
func CanonicalPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("trust: path is required")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("trust: path contains a null byte")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("trust: resolve path %q: %w", path, err)
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("trust: resolve symlinks for %q: %w", path, err)
	}
	return filepath.Clean(physical), nil
}

// ParentPath returns the parent of path. The second result is false when path
// is a filesystem root and has no parent.
func ParentPath(path string) (string, bool) {
	parent := filepath.Dir(path)
	if parent == path {
		return "", false
	}
	return parent, true
}
