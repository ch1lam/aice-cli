// Package skill discovers and parses Agent Skills.
//
// Every skill is the same resource on one processing path. Source is a
// metadata label used for display and same-name conflict ordering; it does
// not change scan, parse, validation, or activation behavior.
package skill

// Source labels where a skill was found. Values carry no permission or
// behavior semantics.
type Source string

const (
	// SourceBuiltin is a skill embedded in the AICE binary.
	SourceBuiltin Source = "builtin"
	// SourceUser is a skill installed in the user's global skills root.
	SourceUser Source = "user"
	// SourceProject is a skill installed in the workspace skills root.
	SourceProject Source = "project"
)

// String returns the stable source label.
func (s Source) String() string {
	switch s {
	case SourceBuiltin, SourceUser, SourceProject:
		return string(s)
	default:
		return "unknown"
	}
}

func (s Source) rank() int {
	switch s {
	case SourceProject:
		return 3
	case SourceUser:
		return 2
	case SourceBuiltin:
		return 1
	default:
		return 0
	}
}

// Skill is one discovered Agent Skill.
type Skill struct {
	// Name is the frontmatter name.
	Name string
	// Description is the frontmatter description.
	Description string
	// Source is a metadata label for display and conflict ordering.
	Source Source
	// Dir is the absolute skill directory on disk. It is empty for
	// embedded skills, which have no host path.
	Dir string
	// Body is the Markdown after YAML frontmatter.
	Body string
}

// Level is the severity of a Diagnostic.
type Level string

const (
	// LevelWarn is a non-fatal issue; the skill is still loaded when
	// applicable.
	LevelWarn Level = "warn"
	// LevelError is a fatal issue for one skill; that skill is skipped.
	LevelError Level = "error"
)

// Diagnostic records a discovery or parse issue for one skill directory.
type Diagnostic struct {
	Level   Level
	Dir     string
	Message string
}

// Catalog is a merged, deterministically ordered set of skills after
// same-name shadowing.
type Catalog struct {
	skills []Skill
	byName map[string]Skill
}

// Skills returns skills sorted by name. The slice is a copy.
func (c Catalog) Skills() []Skill {
	out := make([]Skill, len(c.skills))
	copy(out, c.skills)
	return out
}

// Lookup returns the loaded skill with the given frontmatter name.
func (c Catalog) Lookup(name string) (Skill, bool) {
	item, ok := c.byName[name]
	return item, ok
}
