package guard

import (
	"os"
	"path/filepath"
	"strings"
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

// isWithinBoundary reports whether abs is inside cwd (including cwd itself).
func isWithinBoundary(abs, cwd string) bool {
	if cwd == "" || abs == "" {
		return false
	}
	abs = filepath.Clean(abs)
	cwd = filepath.Clean(cwd)
	if abs == cwd {
		return true
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func resolveForDisplay(path, cwd string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return rel
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		if rel, err := filepath.Rel(home, path); err == nil {
			return filepath.Join("~", rel)
		}
	}
	return path
}

func resolveAllowedPath(p AllowedPath, cwd string) string {
	raw := strings.TrimSpace(p.Path)
	if raw == "" {
		return ""
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			raw = filepath.Join(home, raw[1:])
		}
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if cwd != "" {
		return filepath.Clean(filepath.Join(cwd, raw))
	}
	return filepath.Clean(raw)
}

func isPathAllowed(abs string, allowed []AllowedPath, cwd string, sessionAllowed map[string]bool) bool {
	abs = filepath.Clean(abs)
	// Session grants (in-memory)
	if sessionAllowed[abs] {
		return true
	}
	// Also check if any session dir grant covers this file
	for k := range sessionAllowed {
		// sessionAllowed tracks both file and directory grants via the same map;
		// for path-access we store directory grants as the directory path itself,
		// and membership test will catch exact matches only. The directory-cover
		// check below handles descendant coverage.
		if strings.HasPrefix(k, "dir:") {
			dir := strings.TrimPrefix(k, "dir:")
			if isWithinBoundary(abs, dir) {
				return true
			}
		}
	}
	for _, entry := range allowed {
		resolved := resolveAllowedPath(entry, cwd)
		if resolved == "" {
			continue
		}
		switch entry.Kind {
		case "directory":
			if isWithinBoundary(abs, resolved) {
				return true
			}
		default: // file
			if abs == resolved {
				return true
			}
		}
	}
	return false
}

func isGrantTooBroad(abs string) bool {
	cleaned := filepath.Clean(abs)
	if cleaned == "/" {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && filepath.Clean(home) == cleaned {
		return true
	}
	return false
}
