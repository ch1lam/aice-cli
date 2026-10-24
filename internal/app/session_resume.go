package app

import (
	"context"
	"errors"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/session"
)

func (s *interactiveSession) slashResume(ctx context.Context, request interaction.CommandRequest) (string, error) {
	path, err := s.sessionSelection(request.Arguments)
	if err != nil {
		return "", err
	}
	// RunSlashCommand holds an idle settings reservation, so no main or side
	// run can start across the switch. Side creation also acquires
	// historySyncMu before capturing its parent snapshot.
	c := &s.conversation
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	if c.store != nil && c.store.Path() == path {
		snapshot, err := c.store.Snapshot()
		if err != nil {
			return "", err
		}
		view, err := sessionTranscript(snapshot)
		if err != nil {
			return "", err
		}
		s.publishTranscript(view)
		return "Current session restored", nil
	}
	// Validate the workspace and supported format without repairing anything.
	if _, _, err := s.readSelectedSession(ctx, request.Arguments); err != nil {
		return "", err
	}
	store, _, err := openExistingSession(ctx, s.workspace, path)
	if err != nil {
		return "", err
	}
	if err := store.RecoverInterrupted(ctx); err != nil {
		return "", errors.Join(err, store.Close())
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		return "", errors.Join(err, store.Close())
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		return "", errors.Join(err, store.Close())
	}
	view, err := sessionTranscript(snapshot)
	if err != nil {
		return "", errors.Join(err, store.Close())
	}
	if err := ctx.Err(); err != nil {
		return "", errors.Join(err, store.Close())
	}
	previous := c.store
	c.store = store
	c.historyLeaf, c.historyReady = snapshot.LeafID, true
	c.historyMu.Lock()
	c.history = history
	c.historyMu.Unlock()
	clear(s.sideThreads)
	view.ResetSideThreads = true
	if s.guard != nil {
		s.guard.ResetSessionGrants()
	}
	s.stateMu.Lock()
	s.totalUsage = session.TotalUsage(snapshot)
	s.stateMu.Unlock()
	s.publishTranscript(view)
	// Once installed, cleanup failures are warnings, not a failed switch.
	cleanupErr := closeInteractiveStore(previous)
	output := "Resumed session " + shortSessionID(snapshot.Header.ID)
	if cleanupErr != nil {
		output += "\nCleanup warning: " + cleanupErr.Error()
	}
	return output, nil
}

func (s *interactiveSession) publishTranscript(view *interaction.Transcript) {
	s.stateMu.Lock()
	s.transcript = view
	s.sessionChanged = true
	s.stateMu.Unlock()
}
