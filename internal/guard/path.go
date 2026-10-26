package guard

import (
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/hostpath"
)

func resolveForDisplay(path, cwd string) string {
	path = filepath.Clean(path)
	if hostpath.Within(cwd, path) {
		if rel, err := filepath.Rel(cwd, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return hostpath.HomeDisplay(path)
}

func resolveReadOnlyRoots(roots []string, cwd string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if root = strings.TrimSpace(root); root == "" {
			continue
		}
		out = append(out, resolveAbsolute(root, cwd, ""))
	}
	return out
}

func inReadOnlyRoot(abs string, roots []string) bool {
	if abs == "" {
		return false
	}
	abs = filepath.Clean(abs)
	for _, root := range roots {
		if hostpath.Within(root, abs) {
			return true
		}
	}
	return false
}

func isGrantTooBroad(path string) bool {
	cleaned := filepath.Clean(path)
	if isFilesystemRoot(cleaned) {
		return true
	}
	if rel, ok := hostpath.UnderHome(cleaned); ok && rel == "." {
		return true
	}
	return false
}

// isFilesystemRoot reports whether cleaned (already filepath.Clean'd) is a
// filesystem root. After Clean, roots satisfy Dir(p) == p: Unix "/", Windows
// drive roots ("C:\\"), and UNC share roots ("\\\\server\\share"). "." also
// satisfies that identity, so relative paths are excluded via IsAbs. On
// Windows, Clean("/") and Clean("\\") yield "\\", the current-drive root,
// which filepath.IsAbs reports as false.
func isFilesystemRoot(cleaned string) bool {
	if filepath.IsAbs(cleaned) {
		return filepath.Dir(cleaned) == cleaned
	}
	return cleaned == string(filepath.Separator)
}

// GrantTooBroad reports whether a path-access grant would cover the
// filesystem root or the user's home directory.
func GrantTooBroad(path string) bool {
	return isGrantTooBroad(path)
}
