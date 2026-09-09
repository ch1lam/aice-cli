package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveMutationPath follows existing links before processing parent traversal.
// Missing components are returned for write; edit separately requires a regular file.
func (w *Workspace) resolveMutationPath(input string) (string, error) {
	path, err := w.resolvePath(input)
	if err != nil {
		return "", err
	}
	if os.IsPathSeparator(path[len(path)-1]) {
		return "", fmt.Errorf("resolve mutation path %q: file path ends with a separator", input)
	}
	volume := filepath.VolumeName(path)
	current := volume + string(os.PathSeparator)
	parts := strings.FieldsFunc(path[len(volume):], func(r rune) bool {
		return r == '/' || (os.PathSeparator == '\\' && r == '\\')
	})
	for index, part := range parts {
		// Keep traversal until existing symlinks have been resolved. Cleaning the
		// original path first would give link/../file the wrong destination.
		candidate := current + string(os.PathSeparator) + part
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			for _, remaining := range parts[index:] {
				if remaining == "." || remaining == ".." {
					return "", fmt.Errorf("resolve mutation path %q: traversal through a missing directory", input)
				}
			}
			return filepath.Join(append([]string{current}, parts[index:]...)...), nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve mutation path %q: %w", input, err)
		}
		current = candidate
		if info.Mode()&os.ModeSymlink != 0 {
			current, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve mutation symlink %q: %w", input, err)
			}
			info, err = os.Stat(current)
			if err != nil {
				return "", fmt.Errorf("inspect mutation target %q: %w", input, err)
			}
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("resolve mutation path %q: parent is not a directory", input)
		}
	}
	return filepath.Clean(current), nil
}
