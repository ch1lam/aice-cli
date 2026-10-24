package interaction

import "context"

// Frontend is the application surface offered to one interactive frontend.
// Runner is required. A nil optional capability hides its feature.
type Frontend struct {
	Runner          Runner
	State           RuntimeStateProvider
	Commands        CommandRunner
	SideThreads     SideThreadManager
	Sessions        SessionBrowser
	Usage           UsageReader
	Settings        SettingsReader
	SettingsActions SettingsActionRunner
	SettingsWriter  SettingsWriter
	Files           FileCompleter
	Guards          GuardRequester
	Questions       QuestionRequester
	// CheckUpdate, when set, performs the welcome-screen update check.
	CheckUpdate UpdateChecker
}

// UpdateCheckStatus identifies one update-check result.
type UpdateCheckStatus uint8

const (
	// UpdateCheckStatusUnknown is the unset state.
	UpdateCheckStatusUnknown UpdateCheckStatus = iota
	// UpdateCheckStatusDisabled means automatic checks are disabled.
	UpdateCheckStatusDisabled
	// UpdateCheckStatusDevelopment means this build has no comparable version.
	UpdateCheckStatusDevelopment
	// UpdateCheckStatusCurrent means no newer release is available.
	UpdateCheckStatusCurrent
	// UpdateCheckStatusAvailable means Latest is newer than this build.
	UpdateCheckStatusAvailable
)

// UpdateCheckResult is one update-check outcome.
type UpdateCheckResult struct {
	Status UpdateCheckStatus
	Latest string
}

// UpdateChecker performs one cache-aware, bounded update check.
type UpdateChecker func(ctx context.Context) (UpdateCheckResult, error)
