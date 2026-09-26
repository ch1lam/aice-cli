package interaction

import (
	"context"
	"time"
)

// UsageSnapshot is a read-only projection; it never consumes transcript state.
type UsageSnapshot struct {
	Revision                           uint64
	ReadAt                             time.Time
	SessionID, Path, Directory, LeafID string
	Provider, Model, WindowSource      string
	CreatedAt                          int64
	Nodes, Messages, Compactions       int
	Context                            DisplayContext
	Usage                              DisplayUsage
	ReasoningTokens                    int64
	CostStatus                         string
	Scope                              string
}

type UsageReader interface {
	ReadUsage(context.Context) (UsageSnapshot, error)
}
