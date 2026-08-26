// Package hostpath provides host filesystem path membership, tilde expansion,
// and slash-normalized display. Callers do not branch on GOOS; Windows
// membership is case-insensitive, matching internal/trust store keys.
package hostpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Within reports whether child is inside parent, including when they name
// the same path. Different volumes return false.
func Within(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parent = fold(filepath.Clean(parent))
	child = fold(filepath.Clean(child))
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Same reports whether a and b name the same path after Clean.
func Same(a, b string) bool {
	return fold(filepath.Clean(a)) == fold(filepath.Clean(b))
}

// UnderHome reports whether path is the user's home directory or a descendant.
// rel is filepath.Rel(home, path), or "." when path is home itself.
func UnderHome(path string) (rel string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	if !Within(home, path) {
		return "", false
	}
	rel, err = filepath.Rel(home, path)
	if err != nil {
		rel, err = filepath.Rel(fold(home), fold(path))
		if err != nil {
			return "", false
		}
	}
	return rel, true
}

// ExpandTilde expands "~", "~/", and "~\". The home prefix is joined with
// path[1:] so a leading separator in the remainder does not drop home.
func ExpandTilde(path string) string {
	if !hasTildePrefix(path) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, path[1:])
}

// IsBashRooted reports whether path uses Git Bash root syntax (a leading
// "/"). On Windows, filepath.IsAbs is false for these without a drive.
func IsBashRooted(path string) bool {
	return strings.HasPrefix(path, "/")
}

// HomeDisplay shortens a path under the user home to "~/..." and converts
// separators to "/" for user-facing text.
func HomeDisplay(path string) string {
	path = filepath.Clean(path)
	rel, ok := UnderHome(path)
	if !ok {
		return filepath.ToSlash(path)
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

func hasTildePrefix(path string) bool {
	switch {
	case path == "~":
		return true
	case strings.HasPrefix(path, "~/"):
		return true
	case strings.HasPrefix(path, `~\`):
		return true
	default:
		return false
	}
}

func fold(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	return strings.ToLower(path)
}
