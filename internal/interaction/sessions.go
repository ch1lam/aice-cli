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
	// ScanSessions returns the full catalog. When publish is non-nil it
	// receives independently owned partial catalogs synchronously on the
	// scanning goroutine and may cancel scanning by returning an error.
	ScanSessions(context.Context, string, func([]SessionSummary) error) ([]SessionSummary, error)
	PreviewSession(context.Context, string, string) (string, error)
	// ReadSession projects a branch for inspection without changing the saved
	// active leaf, acquiring writer ownership, or recovering interrupted tools.
	ReadSession(context.Context, string, string) (*SessionReading, error)
	// RenameSession appends title metadata to an existing session. It must
	// preserve the live conversation, active branch and all source records.
	RenameSession(context.Context, string, string) (SessionSummary, error)
}

// SessionTitle announces a title the application saved for a Session.
type SessionTitle struct {
	SessionID, Title string
}

// SessionTitleSource announces generated Session titles. Delivery is best
// effort: an announcement nobody receives promptly is dropped, and the saved
// title still appears wherever the Session catalog is read.
type SessionTitleSource interface {
	SessionTitles() <-chan SessionTitle
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
	// TranscriptNotification is an AICE notification the model received, such
	// as finished background tasks; Text is its summary.
	TranscriptNotification
)

// TranscriptEntry is presentation data, never an executable Agent event.
type TranscriptEntry struct {
	ID        string
	Kind      TranscriptKind
	Text      string
	Assistant AssistantDisplay
	Tool      ToolDisplay
}
