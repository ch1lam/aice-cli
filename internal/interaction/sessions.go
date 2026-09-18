package interaction

import "context"

// SessionSummary is a derived, read-only catalog entry. Key is an opaque
// project-local selection token; frontends must not interpret it as a path.
type SessionSummary struct {
	Key, ID, Title string
	UpdatedAt      int64
	Snippet        string
	Problem        string
	MatchID        string
	OtherBranch    bool
	TitleMatch     bool
}

// SessionBrowser reads project history independently of the active-run
// controller. Implementations must honor cancellation and never repair files.
type SessionBrowser interface {
	SearchSessions(context.Context, string) ([]SessionSummary, error)
	PreviewSession(context.Context, string, string) (string, error)
}

// SessionScanner optionally publishes independently owned partial catalogs.
// The callback runs synchronously on the scanning goroutine and may cancel
// scanning by returning an error. The final return contains the full catalog.
type SessionScanner interface {
	ScanSessions(context.Context, string, func([]SessionSummary) error) ([]SessionSummary, error)
}

// SessionRenamer optionally appends title metadata to an existing session.
// It must preserve the live conversation, active branch and all source records.
type SessionRenamer interface {
	RenameSession(context.Context, string, string) (SessionSummary, error)
}

// SessionReader projects a branch for inspection without changing the saved
// active leaf, acquiring writer ownership, or recovering interrupted tools.
type SessionReader interface {
	ReadSession(context.Context, string, string) (*SessionReading, error)
}

// SessionReading contains read-only display projections, with the saved active
// branch available for returning to its latest content.
type SessionReading struct {
	Transcript  *Transcript
	Active      *Transcript
	FocusID     string
	OtherBranch bool
}

// Transcript is the original active branch, independent of compacted model
// context. It is transferred once at startup or after a branch replacement.
type Transcript struct {
	SessionID        string
	Entries          []TranscriptEntry
	ResetSideThreads bool
}

type TranscriptKind uint8

const (
	TranscriptUser TranscriptKind = iota + 1
	TranscriptAssistant
	TranscriptTool
	TranscriptNotice
)

// TranscriptEntry is presentation data, never an executable Agent event.
type TranscriptEntry struct {
	ID        string
	Kind      TranscriptKind
	Text      string
	Assistant AssistantDisplay
	Tool      ToolDisplay
}
