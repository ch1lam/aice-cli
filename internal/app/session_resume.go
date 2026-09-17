package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/session"
)

func (s *interactiveSession) slashResume(ctx context.Context, request interaction.CommandRequest) (string, error) {
	path, err := s.sessionSelection(request.Arguments)
	if err != nil {
		return "", err
	}
	c := &s.conversation
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	c.historyMu.RLock()
	active := c.activeMainRun != nil
	c.historyMu.RUnlock()
	if active {
		return "", fmt.Errorf("app: stop the current response before resuming a session")
	}
	// Prevent a held side runner from starting across the switch. Creation
	// acquires historySyncMu before capturing its parent snapshot as well.
	s.sideMu.Lock()
	defer s.sideMu.Unlock()
	if s.sideRunning != 0 {
		return "", fmt.Errorf("app: stop BTW responses before resuming a session")
	}
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
	if s.browser != nil {
		cleanupErr = errors.Join(cleanupErr, closeBrowser(ctx, s.browser), s.browser.Rotate())
		cleanupErr = errors.Join(cleanupErr, applyBrowserEnvironment(s.browser))
	}
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
