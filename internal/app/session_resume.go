package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
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
	reportErr := s.resetSessionScope(ctx, previous, session.TotalUsage(snapshot), view)
	// Once installed, cleanup failures are warnings, not a failed switch.
	cleanupErr := errors.Join(reportErr, closeInteractiveStore(previous))
	output := "Resumed session " + shortSessionID(snapshot.Header.ID)
	if cleanupErr != nil {
		output += "\nCleanup warning: " + cleanupErr.Error()
	}
	return output, nil
}

// resetSessionScope drops what belonged to the previous Session after a switch
// or /new: BTW threads, background tasks, temporary Guard grants and measured
// throughput. Subagents it stops are reported to previous, still open. It
// publishes view, which tells the frontend to discard its side threads too.
// A failed report is returned as a warning; the switch has happened.
func (s *interactiveSession) resetSessionScope(ctx context.Context, previous *session.Store, usage llm.Usage, view *interaction.Transcript) error {
	s.sideMu.Lock()
	clear(s.sideThreads)
	s.sideMu.Unlock()
	reportErr := reportStoppedSubagents(ctx, s.tasks, previous)
	if s.guard != nil {
		s.guard.ResetSessionGrants()
	}
	view.ResetSideThreads = true
	s.stateMu.Lock()
	s.totalUsage = usage
	s.throughput = interaction.DisplayThroughput{}
	s.stateMu.Unlock()
	s.publishTranscript(view)
	return reportErr
}

// reportStoppedSubagents stops every background task and records the
// notification of the subagents no result reported into store, the Session
// that started them, so their reports and usage stay in its history and
// totals. Without a store there is nowhere to keep them.
func reportStoppedSubagents(ctx context.Context, tasks *tool.Tasks, store *session.Store) error {
	notice, ok := tasks.KillAll()
	if !ok || store == nil {
		return nil
	}
	// Leaving a Session, also by an interrupt, still records what it spent.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := appendSessionMessage(ctx, store, notice); err != nil {
		return fmt.Errorf("app: record stopped subagents: %w", err)
	}
	return nil
}

func (s *interactiveSession) publishTranscript(view *interaction.Transcript) {
	s.stateMu.Lock()
	s.transcript = view
	s.sessionChanged = true
	s.stateMu.Unlock()
}
