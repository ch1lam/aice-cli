package interaction

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSettingsBusy    = errors.New("settings or session operation is in progress; retry when it finishes")
	ErrSettingsRunning = errors.New("cannot change settings while a response is running or input is being prepared")
	ErrSettingsStale   = errors.New("settings changed; refresh and retry with the retained draft")
)

// SettingKind selects a frontend control, never an application behavior.
type SettingKind string

const (
	SettingBool     SettingKind = "bool"
	SettingString   SettingKind = "string"
	SettingInt      SettingKind = "int64"
	SettingDuration SettingKind = "duration"
	SettingEnum     SettingKind = "enum"
	SettingContexts SettingKind = "context-windows"
	SettingList     SettingKind = "list"
	SettingAction   SettingKind = "action"
	SettingInfo     SettingKind = "info"
)

// SettingValue carries only public preferences; secrets use AuthInteraction.
type SettingValue struct {
	Kind     SettingKind
	Text     string
	Bool     bool
	Int      int64
	Duration time.Duration
	Contexts []ContextWindowSetting
	List     []string
}

type ContextWindowSetting struct {
	Provider, Model string
	Tokens          int64
}
type SettingCategory struct{ ID, Label string }
type SettingChoice struct{ Value, Label, Description string }
type SettingSource struct{ Kind, Location string }

type SettingTiming string

const (
	SettingNextRun      SettingTiming = "Next agent run"
	SettingNextBrowser  SettingTiming = "Next browser session"
	SettingRestart      SettingTiming = "Next startup"
	SettingDomainAction SettingTiming = "Explicit action"
)

// SettingField is an immutable display projection assembled by the app.
type SettingField struct {
	ID, Category, Label, Description            string
	Keywords                                    []string
	Kind                                        SettingKind
	Value                                       SettingValue
	Saved, Inherited, Default                   *SettingValue
	Source                                      SettingSource
	InheritanceError, DisabledReason, Effective string
	Choices                                     []SettingChoice
	AllowCustom                                 bool
	ResetIDs                                    []string
	DefaultChanges                              []SettingChange
	InvertBool                                  bool
	Applies                                     SettingTiming
	// Action handles action rows, or the explicit enable flow of an off boolean.
	Action    *Command
	Arguments string
}

type SettingsSnapshot struct {
	Revision    uint64
	Runtime     RuntimeState
	Categories  []SettingCategory
	Fields      []SettingField
	SavePath    string
	Diagnostics []string
}

type SettingChange struct {
	ID    string
	Unset bool
	Value SettingValue
}
type SettingsRequest struct {
	Revision uint64
	Changes  []SettingChange
}

type SettingsResult struct {
	Committed, Applied bool
	Revision           uint64
	Applies            SettingTiming
	FieldErrors        map[string]string
	Warnings           []string
}

type SettingsReader interface {
	ReadSettings(context.Context) (SettingsSnapshot, error)
}
type SettingsWriter interface {
	ApplySettings(context.Context, SettingsRequest) (SettingsResult, error)
}

// Validate rejects mismatched payloads before a domain interprets a value.
func (v SettingValue) Validate() error {
	switch v.Kind {
	case SettingBool:
		v.Bool = false
	case SettingString, SettingEnum:
		v.Text = ""
	case SettingInt:
		v.Int = 0
	case SettingDuration:
		v.Duration = 0
	case SettingContexts:
		v.Contexts = nil
	case SettingList:
		v.List = nil
	default:
		return fmt.Errorf("unsupported preference kind %s", v.Kind)
	}
	if v.Text != "" || v.Bool || v.Int != 0 || v.Duration != 0 || v.Contexts != nil || v.List != nil {
		return fmt.Errorf("unexpected preference payload")
	}
	return nil
}

// SettingsActionRunner keeps domain actions outside the transcript path.
type SettingsActionRunner interface {
	RunSettingsAction(context.Context, uint64, CommandRequest) (SettingsActionResult, error)
}

// SettingsActionResult preserves completed external work when a later step
// fails. Ready is meaningful only when ReadinessKnown is true; saving a
// preference does not establish that a native capability is available.
type SettingsActionResult struct {
	Continuation       *TaskContinuation
	Output             string
	External           []SettingsActionStep
	Committed, Applied bool
	ReadinessKnown     bool
	Ready              bool
	Revision           uint64
	Warnings           []string
}

type SettingsActionStep struct {
	Name, Detail string
	Completed    bool
}
