package trust

import (
	"fmt"
	"strings"
)

// AskFunc presents the trust choices to the user and returns the selection.
// It is nil when no interactive UI is available.
type AskFunc func(cwd string) (Choice, error)

// Choice is one selectable trust option, mirroring Pi's ProjectTrustOption.
// Updates carries the store mutations to persist once the choice is applied;
// it is empty for session-only choices.
type Choice struct {
	Label    string
	Decision Decision
	Updates  []Update
	SavePath string
}

// Choices returns the trust options presented for a directory: trust the
// directory, trust its parent, trust for this session only, deny, and deny for
// this session only. The parent option only exists when the directory is not a
// filesystem root.
func Choices(cwd string) []Choice {
	choices := []Choice{
		{
			Label:    "Trust",
			Decision: DecisionTrusted,
			Updates:  []Update{{Path: cwd, Decision: DecisionTrusted}},
			SavePath: cwd,
		},
	}
	if parent, ok := ParentPath(cwd); ok {
		choices = append(choices, Choice{
			Label:    "Trust parent folder (" + parent + ")",
			Decision: DecisionTrusted,
			Updates: []Update{
				{Path: parent, Decision: DecisionTrusted},
				{Path: cwd, Decision: DecisionUnknown},
			},
			SavePath: parent,
		})
	}
	choices = append(choices,
		Choice{Label: "Trust (this session only)", Decision: DecisionTrusted},
		Choice{
			Label:    "Do not trust",
			Decision: DecisionUntrusted,
			Updates:  []Update{{Path: cwd, Decision: DecisionUntrusted}},
			SavePath: cwd,
		},
		Choice{Label: "Do not trust (this session only)", Decision: DecisionUntrusted},
	)
	return choices
}

// ResolveOptions contains every input needed to resolve one trust decision.
type ResolveOptions struct {
	// CWD is the canonical workspace directory used as the trust key.
	CWD string
	// Snapshot lists the protected resources found in the workspace.
	Snapshot Snapshot
	// Override is a --approve or --no-approve flag; nil means unset.
	Override *bool
	// Policy is the global defaultProjectTrust policy.
	Policy Default
	// AskUI shows the interactive choices; nil means no UI is available.
	AskUI AskFunc
}

// Resolution is the outcome of one trust resolution.
type Resolution struct {
	Decision Decision
	Source   Source
	// Entry is the matched stored path when Source is SourceStore.
	Entry Entry
	// Choice is the selected option when Source is SourceInteractive.
	Choice Choice
	// Prompted reports whether an interactive prompt was shown.
	Prompted bool
}

// Resolve determines the effective trust decision for one startup, mirroring
// Pi's resolveProjectTrusted precedence: command-line override, protected
// resources present, saved store decision, default policy, then an interactive
// prompt when the policy is ask and a UI is available.
func (s *Store) Resolve(options ResolveOptions) (Resolution, error) {
	if s == nil {
		return Resolution{}, fmt.Errorf("trust: store is required")
	}
	if strings.TrimSpace(options.CWD) == "" {
		return Resolution{}, fmt.Errorf("trust: workspace path is required")
	}
	if options.Override != nil {
		return Resolution{
			Decision: decisionFromBool(*options.Override),
			Source:   SourceOverride,
		}, nil
	}
	if !options.Snapshot.HasProtected() {
		return Resolution{Decision: DecisionTrusted, Source: SourcePolicy}, nil
	}
	policy := options.Policy
	if policy == "" {
		policy = DefaultAsk
	}

	entry, found, err := s.Lookup(options.CWD)
	if err != nil {
		return Resolution{}, err
	}
	if found {
		return Resolution{
			Decision: entry.Decision,
			Source:   SourceStore,
			Entry:    entry,
		}, nil
	}

	switch policy {
	case DefaultAlways:
		return Resolution{Decision: DecisionTrusted, Source: SourcePolicy}, nil
	case DefaultNever:
		return Resolution{Decision: DecisionUntrusted, Source: SourcePolicy}, nil
	}

	if options.AskUI == nil {
		return Resolution{Decision: DecisionUntrusted, Source: SourcePolicy}, nil
	}
	choice, err := options.AskUI(options.CWD)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{
		Decision: choice.Decision,
		Source:   SourceInteractive,
		Choice:   choice,
		Prompted: true,
	}, nil
}
