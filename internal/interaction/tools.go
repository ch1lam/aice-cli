package interaction

import (
	"path/filepath"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ToolKind is the category of a tool call shared by machine-readable
// frontends. Values follow the Agent Client Protocol vocabulary.
type ToolKind string

const (
	ToolKindRead    ToolKind = "read"
	ToolKindEdit    ToolKind = "edit"
	ToolKindExecute ToolKind = "execute"
	ToolKindFetch   ToolKind = "fetch"
	ToolKindOther   ToolKind = "other"
)

// ToolStatus is the lifecycle state of a tool call in the same vocabulary.
type ToolStatus string

const (
	ToolStatusInProgress ToolStatus = "in_progress"
	ToolStatusCompleted  ToolStatus = "completed"
	ToolStatusFailed     ToolStatus = "failed"
)

// ToolLocation is one absolute file path a tool call reads or writes.
type ToolLocation struct {
	Path string `json:"path"`
}

// ToolKindOf derives a call's kind from the Guard's normalized actions, the
// only authoritative description of what a call does. Tool names and
// tool-reported metadata never decide it. A command dominates the file actions
// extracted from it.
func ToolKindOf(actions []llm.ToolAction) ToolKind {
	kind := ToolKindOther
	for _, action := range actions {
		switch action.Kind {
		case "command":
			return ToolKindExecute
		case "network":
			kind = ToolKindFetch
		case "file":
			if action.Op == "write" {
				kind = ToolKindEdit
			} else if kind != ToolKindEdit {
				kind = ToolKindRead
			}
		}
	}
	return kind
}

// ToolLocations lists the distinct file paths of actions, resolving relative
// paths against workspace.
func ToolLocations(actions []llm.ToolAction, workspace string) []ToolLocation {
	var locations []ToolLocation
	seen := make(map[string]bool)
	for _, action := range actions {
		if action.Kind != "file" || action.Path == "" {
			continue
		}
		path := action.Path
		if !filepath.IsAbs(path) && workspace != "" {
			path = filepath.Join(workspace, path)
		}
		if !seen[path] {
			seen[path] = true
			locations = append(locations, ToolLocation{Path: path})
		}
	}
	return locations
}
