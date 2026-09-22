package agent

import "strings"

// reasoningReplayRejected reports whether err is the gateway rejection of a
// replayed reasoning item. The OpenCode gateways proxy the Muse Spark lane to
// an upstream that binds reasoning.encrypted_content to its own context:
// while that context lives, a stored item replays fine, but once it rotates
// the gateway rejects the replay with "reasoning `encrypted_content` was not
// issued to this caller". Degradation is armed only after the first actual
// rejection, so healthy sessions keep reasoning continuity.
func reasoningReplayRejected(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "encrypted_content") &&
		strings.Contains(text, "was not issued")
}

// armReasoningReplayRetry arms per-run reasoning-history degradation after the
// first replayed-reasoning rejection and reports whether the caller should
// retry the turn once with the degraded request. Once armed, every request in
// this run filters reasoning items out of assistant history, so a poisoned
// session self-heals instead of failing on every continue.
func (e *runExecution) armReasoningReplayRetry(err error) bool {
	if e.degradeReasoning || !reasoningReplayRejected(err) {
		return false
	}
	e.degradeReasoning = true
	return true
}
