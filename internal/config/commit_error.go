package config

import "errors"

// CommittedError is a legacy writer's successful replacement with a cleanup
// warning. Callers must publish prepared state before reporting the warning.
type CommittedError struct{ Warning error }

func (e *CommittedError) Error() string { return "saved; lock cleanup warning: " + e.Warning.Error() }
func (e *CommittedError) Unwrap() error { return e.Warning }
func WasCommitted(err error) bool       { var committed *CommittedError; return errors.As(err, &committed) }
func legacyCommitError(result CommitResult, err error) error {
	if result.Committed && result.CleanupWarning != nil {
		return &CommittedError{Warning: result.CleanupWarning}
	}
	return errors.Join(err, result.CleanupWarning)
}
