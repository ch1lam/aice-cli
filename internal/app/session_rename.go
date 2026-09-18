package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/session"
)

// RenameSession owns only display metadata. Serialize with history switches
// and use the existing writer for the current session; never recover tools,
// switch branches, publish a transcript, or reset live state as a side effect.
func (s *interactiveSession) RenameSession(ctx context.Context, key, title string) (interaction.SessionSummary, error) {
	id, err := session.NewID()
	if err != nil {
		return interaction.SessionSummary{}, err
	}
	record, err := session.NewTitle(id, title, time.Now().UnixMilli())
	if err != nil {
		return interaction.SessionSummary{}, err
	}
	c := &s.conversation
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	if err := ctx.Err(); err != nil {
		return interaction.SessionSummary{}, err
	}
	c.historyMu.RLock()
	active := c.activeMainRun != nil
	c.historyMu.RUnlock()
	if active {
		return interaction.SessionSummary{}, fmt.Errorf("app: stop the current response before renaming")
	}
	path, err := s.sessionSelection(key)
	if err != nil {
		return interaction.SessionSummary{}, err
	}
	store := c.store
	owned := store != nil && store.Path() == path
	if !owned {
		// Inspect without repairing, then revalidate the header under writer lock.
		if _, _, err := s.readSelectedSession(ctx, key); err != nil {
			return interaction.SessionSummary{}, err
		}
		store, err = session.OpenComplete(ctx, path)
		if err != nil {
			return interaction.SessionSummary{}, sessionReadError(path, err)
		}
	}
	info, err := store.Info()
	if err == nil && filepath.Clean(info.Header.WorkingDirectory) != s.workspace.Path() {
		err = fmt.Errorf("app: session belongs to another working directory")
	}
	if err == nil {
		err = store.AppendTitle(ctx, record)
	}
	if !owned {
		err = errors.Join(err, store.Close())
	}
	if err != nil {
		return interaction.SessionSummary{}, err
	}
	// The append changes size/mtime, so normal catalog validation drops stale
	// titles before this response and before subsequent searches or previews.
	entry, err := s.catalogSession(ctx, key)
	if err != nil {
		return interaction.SessionSummary{}, err
	}
	return entry.summary, nil
}
