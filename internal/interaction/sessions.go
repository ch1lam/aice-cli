package interaction

import "context"

// SessionSummary is a derived, read-only catalog entry. Key is an opaque
// project-local selection token; frontends must not interpret it as a path.
type SessionSummary struct {
	Key, ID, Title string
	UpdatedAt      int64
	Snippet        string
	Problem        string
}

// SessionBrowser reads project history independently of the active-run
// controller. Implementations must honor cancellation and never repair files.
type SessionBrowser interface {
	SearchSessions(context.Context, string) ([]SessionSummary, error)
	PreviewSession(context.Context, string, string) (string, error)
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
