package guard

import (
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/hostpath"
)

// PathAccessMode controls outside-workspace access.
type PathAccessMode string

const (
	PathAccessAllow PathAccessMode = "allow"
	PathAccessAsk   PathAccessMode = "ask"
	PathAccessBlock PathAccessMode = "block"
)

// AllowedPath grants access outside the workspace.
type AllowedPath struct {
	Kind string `json:"kind"` // "file" or "directory"
	Path string `json:"path"`
}

// PathAccessConfig mirrors pi-guardrails pathAccess section.
type PathAccessConfig struct {
	Mode         *PathAccessMode `json:"mode,omitempty"`
	AllowedPaths []AllowedPath   `json:"allowedPaths,omitempty"`
}

func (c PathAccessConfig) modeOrDefault() PathAccessMode {
	if c.Mode == nil {
		return PathAccessAsk
	}
	switch *c.Mode {
	case PathAccessAllow, PathAccessAsk, PathAccessBlock:
		return *c.Mode
	default:
		return PathAccessAsk
	}
}

func resolveForDisplay(path, cwd string) string {
	path = filepath.Clean(path)
	if hostpath.Within(cwd, path) {
		if rel, err := filepath.Rel(cwd, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return hostpath.HomeDisplay(path)
}

func resolveAllowedPath(p AllowedPath, cwd string) string {
	raw := strings.TrimSpace(p.Path)
	if raw == "" {
		return ""
	}
	raw = hostpath.ExpandTilde(raw)
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if cwd != "" {
		return filepath.Clean(filepath.Join(cwd, raw))
	}
	return filepath.Clean(raw)
}

func resolveReadOnlyRoots(roots []string, cwd string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		resolved := resolveAllowedPath(AllowedPath{Kind: "directory", Path: root}, cwd)
		if resolved == "" {
			continue
		}
		out = append(out, resolved)
	}
	return out
}

func isPathAccessReadTool(name string) bool {
	switch name {
	case "read", "grep", "find", "ls":
		return true
	default:
		return false
	}
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

func isPathAllowed(abs string, allowed []AllowedPath, cwd string, sessionAllowed map[string]bool) bool {
	abs = filepath.Clean(abs)
	if sessionAllowed[abs] {
		return true
	}
	for k := range sessionAllowed {
		if strings.HasPrefix(k, "dir:") {
			dir := strings.TrimPrefix(k, "dir:")
			if hostpath.Within(dir, abs) {
				return true
			}
			continue
		}
		if hostpath.Same(k, abs) {
			return true
		}
	}
	for _, entry := range allowed {
		resolved := resolveAllowedPath(entry, cwd)
		if resolved == "" {
			continue
		}
		switch entry.Kind {
		case "directory":
			if hostpath.Within(resolved, abs) {
				return true
			}
		default: // file
			if hostpath.Same(abs, resolved) {
				return true
			}
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
