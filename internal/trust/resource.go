package trust

import (
	"fmt"
	"os"
	"path/filepath"
)

// Protected resource names at the workspace root. Only these files are gated
// by project trust; the matching global user files are always trusted.
// Forward-slash names work on every platform; os.Root and os.Open accept
// them on Windows.
const (
	AgentsFile       = "AGENTS.md"
	SystemFile       = ".aice/SYSTEM.md"
	AppendSystemFile = ".aice/APPEND_SYSTEM.md"
)

var protectedResources = []string{
	AgentsFile,
	SystemFile,
	AppendSystemFile,
}

// Resource is one protected project-local file found during discovery.
type Resource struct {
	// Name is the workspace-relative path, for example ".aice/SYSTEM.md".
	Name string
	// Path is the absolute path of the resource.
	Path string
}

// Snapshot is an immutable record of the protected resources present in one
// workspace during startup.
type Snapshot struct {
	Resources []Resource
}

// HasProtected reports whether the snapshot contains any protected resource.
func (s Snapshot) HasProtected() bool {
	return len(s.Resources) > 0
}

// Discover records which protected resources exist under root without reading
// their content. Only regular files count; a directory or device named like a
// resource cannot inject content and does not trigger trust.
func Discover(root string) (Snapshot, error) {
	handle, err := os.OpenRoot(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("trust: open workspace root %q: %w", root, err)
	}
	defer handle.Close()

	resources := make([]Resource, 0, len(protectedResources))
	for _, name := range protectedResources {
		info, err := handle.Stat(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Snapshot{}, fmt.Errorf("trust: inspect %s in %q: %w", name, root, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		resources = append(resources, Resource{
			Name: name,
			Path: filepath.Join(root, name),
		})
	}
	return Snapshot{Resources: resources}, nil
}
