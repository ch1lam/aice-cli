package app

import (
	"errors"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const desktopContinuePrompt = "Continue the current task from its recorded progress. Observe the current application state before acting; do not replay completed actions or repeat actions with an unknown outcome."

var errDesktopContinuationStale = errors.New("app: this continuation no longer matches the task or settings; close Settings and send a new request")

// Called while Settings still owns its reservation. No empty Session is created
// and no prompt is recorded until the user explicitly chooses this proposal.
func (s *interactiveSession) desktopContinuation() *interaction.TaskContinuation {
	c := &s.conversation
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	if c.store == nil {
		return nil
	}
	info, err := c.store.Info()
	if err != nil || !info.HasRecords {
		return nil
	}
	leaf, err := c.store.LeafID()
	if err != nil || leaf == "" {
		return nil
	}
	return &interaction.TaskContinuation{SessionID: info.Header.ID, LeafID: leaf, Prompt: desktopContinuePrompt}
}

// Preparation and Run both check: a prepared run must not outlive the original
// branch, and two preparations of one proposal must not replay it sequentially.
func (s *interactiveSession) validateDesktopContinuation(value *interaction.TaskContinuation) error {
	if value == nil {
		return nil
	}
	revision, _ := s.settingsStatus()
	if value.Revision != revision || value.Prompt != desktopContinuePrompt || !s.settingsSnapshot().configuration.DesktopEnabled {
		return errDesktopContinuationStale
	}
	current := s.desktopContinuation()
	if current == nil || current.SessionID != value.SessionID || current.LeafID != value.LeafID {
		return errDesktopContinuationStale
	}
	return nil
}
