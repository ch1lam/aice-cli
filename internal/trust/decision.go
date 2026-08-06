// Package trust resolves and persists the project trust decisions that gate
// project-local startup resources such as SYSTEM.md and APPEND_SYSTEM.md.
package trust

// Decision is the tri-state trust decision for one directory.
type Decision uint8

const (
	// DecisionUnknown means no decision applies yet and callers must fall
	// back to the default policy or ask the user.
	DecisionUnknown Decision = iota
	// DecisionTrusted allows AICE to read the project's protected resources.
	DecisionTrusted
	// DecisionUntrusted ignores the project's protected resources.
	DecisionUntrusted
)

// decisionFromBool converts a command-line override to a Decision.
func decisionFromBool(value bool) Decision {
	if value {
		return DecisionTrusted
	}
	return DecisionUntrusted
}

// Default is the global defaultProjectTrust policy applied when no stored
// decision and no command-line override exist.
type Default string

const (
	DefaultAsk    Default = "ask"
	DefaultAlways Default = "always"
	DefaultNever  Default = "never"
)

// Valid reports whether d is a supported default policy.
func (d Default) Valid() bool {
	switch d {
	case DefaultAsk, DefaultAlways, DefaultNever:
		return true
	default:
		return false
	}
}

// Source records where a resolved decision came from for diagnostics.
type Source uint8

const (
	// SourceUnknown means the decision was not resolved.
	SourceUnknown Source = iota
	// SourceOverride is a --approve or --no-approve flag for this run.
	SourceOverride
	// SourceStore is a saved trust store entry.
	SourceStore
	// SourcePolicy is the global defaultProjectTrust policy.
	SourcePolicy
	// SourceInteractive is a choice made in the startup trust prompt.
	SourceInteractive
)

// String returns a short human-readable label for a Source.
func (s Source) String() string {
	switch s {
	case SourceOverride:
		return "command-line override"
	case SourceStore:
		return "saved decision"
	case SourcePolicy:
		return "default policy"
	case SourceInteractive:
		return "interactive choice"
	default:
		return "unknown"
	}
}
