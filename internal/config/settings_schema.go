package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// ValueKind describes a preference's storage type. Credentials are never values.
type ValueKind string

const (
	BoolValue           ValueKind = "bool"
	StringValue         ValueKind = "string"
	IntValue            ValueKind = "int64"
	DurationValue       ValueKind = "duration"
	EnumValue           ValueKind = "enum"
	ContextWindowsValue ValueKind = "context-windows"
)

// SettingValue is a small tagged union. Validate rejects unused payloads.
type SettingValue struct {
	Kind     ValueKind
	Text     string
	Bool     bool
	Int      int64
	Duration time.Duration
	Windows  []ContextWindow
}

func (v SettingValue) Validate() error {
	copy := v
	switch v.Kind {
	case BoolValue:
		copy.Bool = false
	case StringValue, EnumValue:
		copy.Text = ""
	case IntValue:
		copy.Int = 0
	case DurationValue:
		copy.Duration = 0
	case ContextWindowsValue:
		copy.Windows = nil
	default:
		return fmt.Errorf("config: unknown value kind %q", v.Kind)
	}
	if copy.Text != "" || copy.Bool || copy.Int != 0 || copy.Duration != 0 || copy.Windows != nil {
		return fmt.Errorf("config: unexpected payload for %s", v.Kind)
	}
	return nil
}

func (v SettingValue) raw() any {
	switch v.Kind {
	case BoolValue:
		return v.Bool
	case IntValue:
		return v.Int
	case DurationValue:
		return v.Duration.String()
	case ContextWindowsValue:
		if v.Windows == nil {
			return []ContextWindow{}
		}
		return slices.Clone(v.Windows)
	default:
		return strings.TrimSpace(v.Text)
	}
}

// SettingDefinition holds config semantics, independent of a frontend.
type SettingDefinition struct {
	ID          Setting
	Kind        ValueKind
	Default     SettingValue
	Environment string
	UserOnly    bool // only the user settings file and explicit runtime patches may supply it
}

// SettingDefinitions returns an independent catalog of editable preferences.
// Collections and credentials retain their dedicated domain operations.
func SettingDefinitions() []SettingDefinition {
	definitions := []SettingDefinition{
		{ID: SettingProvider, Kind: EnumValue},
		{ID: SettingModel, Kind: EnumValue},
		{ID: SettingThinking, Kind: EnumValue, Default: SettingValue{Text: string(llm.DefaultThinkingLevel)}},
		{ID: "default_project_trust", Kind: EnumValue, Default: SettingValue{Text: string(trust.DefaultAsk)}},
		{ID: SettingBrowserHeaded, Kind: BoolValue},
		{ID: SettingDesktopEnabled, Kind: BoolValue, UserOnly: true},
		{ID: SettingDesktopControlMode, Kind: EnumValue, UserOnly: true, Default: SettingValue{Text: string(DesktopBackgroundOnly)}},
		{ID: "no_dep_install", Kind: BoolValue},
		{ID: "no_update_check", Kind: BoolValue},
		{ID: "max_turns", Kind: IntValue},
		{ID: "run_token_budget", Kind: IntValue},
		{ID: "run_no_progress_limit", Kind: IntValue, Default: SettingValue{Int: 8}},
		{ID: "run_timeout", Kind: DurationValue},
		{ID: "context_windows", Kind: ContextWindowsValue},
	}
	env := EnvironmentVariables()
	var endpoints []string
	for key := range env {
		if strings.HasSuffix(key, "_base_url") {
			endpoints = append(endpoints, key)
		}
	}
	slices.Sort(endpoints)
	for _, key := range endpoints {
		definitions = append(definitions, SettingDefinition{ID: Setting(key), Kind: StringValue})
	}
	for i := range definitions {
		definitions[i].Default.Kind = definitions[i].Kind
		definitions[i].Environment = env[string(definitions[i].ID)]
	}
	return definitions
}

func settingDefinition(id Setting) (SettingDefinition, bool) {
	for _, def := range SettingDefinitions() {
		if def.ID == id {
			return def, true
		}
	}
	return SettingDefinition{}, false
}

// ParseSettingValue parses a single editor value using its declared type.
func ParseSettingValue(id Setting, text string) (SettingValue, error) {
	def, ok := settingDefinition(id)
	if !ok {
		return SettingValue{}, fmt.Errorf("config: unsupported setting %q", id)
	}
	value := SettingValue{Kind: def.Kind}
	var err error
	switch def.Kind {
	case BoolValue:
		value.Bool, err = strconv.ParseBool(strings.TrimSpace(text))
	case IntValue:
		value.Int, err = strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case DurationValue:
		value.Duration, err = time.ParseDuration(strings.TrimSpace(text))
	case StringValue, EnumValue:
		value.Text = strings.TrimSpace(text)
	default:
		err = fmt.Errorf("requires a dedicated form")
	}
	if err != nil {
		return SettingValue{}, fmt.Errorf("config: %s: %w", id, err)
	}
	return value, nil
}

// SettingChange distinguishes an explicit zero/empty value from removing the
// user override. Unset also removes this instance's runtime selection.
type SettingChange struct {
	ID    Setting
	Unset bool
	Value SettingValue
}

type SettingsPatch struct{ Changes []SettingChange }

func (p SettingsPatch) validate() error {
	seen := make(map[Setting]bool)
	for _, change := range p.Changes {
		def, ok := settingDefinition(change.ID)
		if !ok {
			return fmt.Errorf("config: unsupported setting %q", change.ID)
		}
		if seen[change.ID] {
			return fmt.Errorf("config: duplicate setting %q", change.ID)
		}
		seen[change.ID] = true
		if change.Unset {
			if change.Value.Kind != "" || change.Value.Text != "" || change.Value.Bool || change.Value.Int != 0 || change.Value.Duration != 0 || change.Value.Windows != nil {
				return fmt.Errorf("config: unset %s must not include a value", change.ID)
			}
			continue
		}
		if change.Value.Kind != def.Kind {
			return fmt.Errorf("config: %s requires %s", change.ID, def.Kind)
		}
		if err := change.Value.Validate(); err != nil {
			return err
		}
	}
	return nil
}
