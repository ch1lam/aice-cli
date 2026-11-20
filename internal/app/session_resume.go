package app

import (
	"context"
	"errors"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func (s *interactiveSession) slashResume(ctx context.Context, request interaction.CommandRequest) (string, error) {
	path, err := s.sessionSelection(request.Arguments)
	if err != nil {
		return "", err
	}
	// RunSlashCommand holds an idle settings reservation, so no main or side
	// run, and no side-thread creation, can start across the switch.
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
	previous := c.installStore(store, history, snapshot.LeafID, true)
	s.resetSessionScope(session.TotalUsage(snapshot), view)
	// Once installed, cleanup failures are warnings, not a failed switch.
	cleanupErr := closeInteractiveStore(previous)
	output := "Resumed session " + shortSessionID(snapshot.Header.ID)
	if cleanupErr != nil {
		output += "\nCleanup warning: " + cleanupErr.Error()
	}
	return output, nil
}

// resetSessionScope drops what belonged to the previous Session after a switch
// or /new: BTW threads, background tasks, temporary Guard grants and measured
// throughput. It publishes view, which tells the frontend to discard its side
// threads too.
func (s *interactiveSession) resetSessionScope(usage llm.Usage, view *interaction.Transcript) {
	s.sideMu.Lock()
	clear(s.sideThreads)
	s.sideMu.Unlock()
	s.tasks.KillAll()
	if s.guard != nil {
		s.guard.ResetSessionGrants()
	}
	view.ResetSideThreads = true
	s.stateMu.Lock()
	s.totalUsage = usage
	s.throughput = interaction.DisplayThroughput{}
	s.stateMu.Unlock()
	s.publishTranscript(view)
}

func (s *interactiveSession) publishTranscript(view *interaction.Transcript) {
	s.stateMu.Lock()
	s.transcript = view
	s.sessionChanged = true
	s.stateMu.Unlock()
}
