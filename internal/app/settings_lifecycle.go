package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// settingsLifecycle coordinates resource owners, not work scheduling. Its lock
// is held only to reserve/release an operation; never across I/O or callbacks.
// No caller may acquire it while holding stateMu or sideMu.
type settingsLifecycle struct {
	mu               sync.Mutex
	revision         uint64
	resourceRevision uint64
	sharedChange     bool
	warnings         []string
	changing         bool
	preparing        int
	mainRunning      bool
	sideRunning      int
}

func (s *interactiveSession) beginSettingsOperation(revision *uint64, shared bool) error {
	l := &s.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changing {
		return interaction.ErrSettingsBusy
	}
	if revision != nil && *revision != l.revision {
		return interaction.ErrSettingsStale
	}
	if shared && (l.preparing > 0 || l.mainRunning || l.sideRunning > 0) {
		return interaction.ErrSettingsRunning
	}
	// Existing embedding consumers can own a conversation directly. Preserve
	// that active-run authority in addition to the application reservation.
	if shared {
		s.conversation.historyMu.RLock()
		active := s.conversation.activeMainRun != nil
		s.conversation.historyMu.RUnlock()
		if active {
			return interaction.ErrSettingsRunning
		}
	}
	l.changing = true
	l.sharedChange = shared
	l.warnings = nil
	return nil
}

func (s *interactiveSession) endSettingsOperation(changed bool) (uint64, []string) {
	s.lifecycle.mu.Lock()
	if changed {
		s.lifecycle.revision++
		if s.lifecycle.sharedChange {
			s.lifecycle.resourceRevision++
			s.sideMu.Lock()
			for _, thread := range s.sideThreads {
				thread.invalidated = true
			}
			s.sideMu.Unlock()
		}
	}
	s.lifecycle.changing = false
	warnings := s.lifecycle.warnings
	s.lifecycle.warnings = nil
	revision := s.lifecycle.revision
	s.lifecycle.mu.Unlock()
	return revision, warnings
}

func (s *interactiveSession) beginPreparation() (uint64, error) {
	l := &s.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changing {
		return 0, interaction.ErrSettingsBusy
	}
	l.preparing++
	return l.resourceRevision, nil
}

func (s *interactiveSession) endPreparation() {
	s.lifecycle.mu.Lock()
	s.lifecycle.preparing--
	s.lifecycle.mu.Unlock()
}

func (s *interactiveSession) reserveMainRun(revision uint64) error {
	l := &s.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changing {
		return interaction.ErrSettingsBusy
	}
	if revision != l.resourceRevision {
		return interaction.ErrSettingsStale
	}
	if l.mainRunning {
		return fmt.Errorf("app: a main response is already running")
	}
	l.mainRunning = true
	return nil
}

func (s *interactiveSession) releaseMainRun() {
	s.lifecycle.mu.Lock()
	s.lifecycle.mainRunning = false
	s.lifecycle.mu.Unlock()
}

func (s *interactiveSession) settingsStatus() (revision uint64, reason string) {
	l := &s.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.statusLocked()
}

func (l *settingsLifecycle) statusLocked() (uint64, string) {
	reason := ""
	switch {
	case l.changing:
		reason = interaction.ErrSettingsBusy.Error()
	case l.preparing > 0 || l.mainRunning || l.sideRunning > 0:
		reason = interaction.ErrSettingsRunning.Error()
	}
	return l.revision, reason
}

func slashChangesResources(request interaction.CommandRequest) (changes, shared bool) {
	switch request.Name {
	case "provider", "model", "thinking", "login", "history", "checkout", "compact", "new", "init":
		return true, true
	case "trust":
		return true, false
	case "web", "browser":
		action := strings.TrimSpace(request.Arguments)
		return action != "" && action != "status", true
	default:
		return false, false
	}
}

func (s *interactiveSession) settingsWarning(err error) {
	s.lifecycle.mu.Lock()
	s.lifecycle.warnings = append(s.lifecycle.warnings, err.Error())
	s.lifecycle.mu.Unlock()
}
