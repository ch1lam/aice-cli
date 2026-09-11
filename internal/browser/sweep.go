package browser

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var ownedSession = regexp.MustCompile(`^aice-([1-9][0-9]*)-([1-9][0-9]*)\.pid$`)

// SweepStale only closes sessions whose AICE owner is demonstrably dead. PID
// reuse is deliberately conservative: an unrelated live owner is left alone.
func (m *Manager) SweepStale(ctx context.Context) []error {
	entries, err := os.ReadDir(m.runDir)
	if err != nil {
		return []error{err}
	}
	var failures []error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return append(failures, ctx.Err())
		}
		match := ownedSession.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid == m.pid || m.alive(pid) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".pid")
		if err := m.closeSession(ctx, name); err != nil {
			failures = append(failures, fmt.Errorf("close stale browser %s: %w", name, err))
		}
	}
	return failures
}
