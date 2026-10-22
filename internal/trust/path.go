package trust

import "path/filepath"

// ParentPath returns the parent of path. The second result is false when path
// is a filesystem root and has no parent.
func ParentPath(path string) (string, bool) {
	parent := filepath.Dir(path)
	if parent == path {
		return "", false
	}
	return parent, true
}
