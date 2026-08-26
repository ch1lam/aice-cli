package trust

import (
	"fmt"
	"os"
	"path/filepath"
)

// Protected resource names at the workspace root. Only these resources are
// gated by project trust; the matching global user files are always trusted.
// AgentsFile, SystemFile, and AppendSystemFile must be regular files.
// SkillsDir must be a directory. Forward-slash names work on every platform;
// os.Root and os.Open accept them on Windows.
const (
	AgentsFile       = "AGENTS.md"
	SystemFile       = ".aice/SYSTEM.md"
	AppendSystemFile = ".aice/APPEND_SYSTEM.md"
	SkillsDir        = ".agents/skills"
)

// resourceKind is the filesystem type Discover requires for a protected
// resource. File resources must be regular files; directory resources must
// be directories. A mismatch (file named like a directory, directory named
// like a file, device) does not trigger trust.
type resourceKind int

const (
	resourceFile resourceKind = iota
	resourceDir
)

type protectedSpec struct {
	name string
	kind resourceKind
}

var protectedResources = []protectedSpec{
	{name: AgentsFile, kind: resourceFile},
	{name: SystemFile, kind: resourceFile},
	{name: AppendSystemFile, kind: resourceFile},
	{name: SkillsDir, kind: resourceDir},
}

// Resource is one protected project-local file or directory found during
// discovery.
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
// their content. File resources count only as regular files; a directory or
// device of the same name cannot inject prompt content and does not trigger
// trust. Directory resources count only as directories, including an empty
// directory: presence signals project skill intent, and content validation
// belongs to skill discovery. Inspection uses os.Root, so a symlink that
// escapes the workspace is an error rather than a match.
func Discover(root string) (Snapshot, error) {
	handle, err := os.OpenRoot(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("trust: open workspace root %q: %w", root, err)
	}
	defer handle.Close()

	resources := make([]Resource, 0, len(protectedResources))
	for _, spec := range protectedResources {
		info, err := handle.Stat(spec.name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Snapshot{}, fmt.Errorf("trust: inspect %s in %q: %w", spec.name, root, err)
		}
		if !spec.matches(info) {
			continue
		}
		resources = append(resources, Resource{
			Name: spec.name,
			Path: filepath.Join(root, spec.name),
		})
	}
	return Snapshot{Resources: resources}, nil
}

func (s protectedSpec) matches(info os.FileInfo) bool {
	switch s.kind {
	case resourceFile:
		return info.Mode().IsRegular()
	case resourceDir:
		return info.IsDir()
	default:
		return false
	}
}
