package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/session"
)

func (s *interactiveSession) ReadSession(ctx context.Context, key, focus string) (*interaction.SessionReading, error) {
	snapshot, _, err := s.readSelectedSession(ctx, key)
	if err != nil {
		return nil, err
	}
	active, err := sessionTranscript(snapshot)
	if err != nil {
		return nil, err
	}
	view := &interaction.SessionReading{Transcript: active, Active: active, FocusID: focus}
	if focus == "" {
		return view, ctx.Err()
	}
	for _, entry := range active.Entries {
		if entry.ID == focus {
			return view, ctx.Err()
		}
	}
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return nil, err
	}
	descendants := map[string]bool{}
	leaf := ""
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if node.ID == focus || descendants[node.ParentID] {
			descendants[node.ID] = true
			leaf = node.ID
		}
	}
	if leaf == "" {
		return nil, fmt.Errorf("app: search match no longer exists")
	}
	// Only this local read snapshot changes. No leaf record is appended.
	snapshot.LeafID = leaf
	view.Transcript, err = sessionTranscript(snapshot)
	view.OtherBranch = true
	return view, err
}
