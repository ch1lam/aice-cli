package tool

import "path/filepath"

// ResolveGrepPaths returns the normalized absolute spelling and physical search
// target. Permission checks need both: a protected alias must remain protected
// even when its symlink target has an ordinary name. Grep execution uses the
// physical target. Unlike read, grep does not probe alternate filename variants.
func (w *Workspace) ResolveGrepPaths(input string) (string, string, error) {
	if input == "" {
		input = "."
	}
	path, err := w.resolvePath(normalizeReadInput(filepath.ToSlash(input)))
	if err != nil {
		return "", "", err
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	return path, physical, nil
}
