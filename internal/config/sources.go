package config

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/viper"
)

// Source identifies a non-secret location that contributed a value.
type Source struct{ Kind, Location string }

type configLayer struct {
	source Source
	values map[string]any
}

type frozenSettings struct {
	layers  []configLayer
	runtime map[string]any
}

func defaultValues() map[string]any {
	values := make(map[string]any)
	for _, def := range SettingDefinitions() {
		values[string(def.ID)] = def.Default.raw()
	}
	return values
}

func newFrozenSettings() *frozenSettings {
	return &frozenSettings{layers: []configLayer{{source: Source{Kind: "default"}, values: defaultValues()}}}
}

func (f *frozenSettings) add(source Source, values map[string]any) {
	// File arrays/objects may subsequently be transformed by Viper. Keep an
	// independent JSON-shaped copy, including invalid shadowed values.
	data := make(map[string]any, len(values))
	for key, value := range values {
		data[strings.ToLower(key)] = cloneSettingRaw(value)
	}
	filterSettingSources(data, source.Kind)
	f.layers = append(f.layers, configLayer{source: source, values: data})
}

func cloneSettingRaw(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = cloneSettingRaw(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneSettingRaw(item)
		}
		return result
	case []ContextWindow:
		return slices.Clone(value)
	default:
		return value
	}
}

func (f *frozenSettings) addInvocation(options LoadOptions) error {
	if options.Environment {
		values := make(map[string]any)
		for key, name := range EnvironmentVariables() {
			if value, ok := os.LookupEnv(name); ok && value != "" {
				values[key] = value
			}
		}
		f.add(Source{Kind: "env"}, values)
	}
	flags, err := newRegistry(LoadOptions{BindFlags: options.BindFlags})
	if err != nil {
		return err
	}
	values := make(map[string]any)
	for key := range EnvironmentVariables() {
		if flags.IsSet(key) {
			values[key] = flags.Get(key)
		}
	}
	f.add(Source{Kind: "flag"}, values)
	return nil
}

func (c Config) frozen() *frozenSettings {
	if c.layers != nil {
		return c.layers
	}
	// Explicitly constructed Config values (embedding callers/tests) are a
	// frozen baseline too. Do not consult files or the process environment.
	values, _ := settingsValues(c.settings())
	f := newFrozenSettings()
	f.add(Source{Kind: "default"}, values)
	return f
}

func (f *frozenSettings) resolved() map[string]any {
	values := make(map[string]any)
	for _, layer := range f.layers {
		maps.Copy(values, layer.values)
	}
	maps.Copy(values, f.runtime)
	return values
}

func decodeSettingMap(values map[string]any) (Config, error) {
	v := viper.New()
	// Set preserves scalar/array replacement and exact model IDs.
	for key, value := range values {
		v.Set(key, cloneSettingRaw(value))
	}
	return decodeEffective(v)
}

func (f *frozenSettings) changed(patch SettingsPatch, path string) *frozenSettings {
	next := &frozenSettings{layers: slices.Clone(f.layers), runtime: maps.Clone(f.runtime)}
	if next.runtime == nil {
		next.runtime = make(map[string]any)
	}
	user := -1
	for i, layer := range next.layers {
		if layer.source.Kind == "user-settings" {
			user = i
			break
		}
	}
	if user < 0 {
		next.layers = slices.Insert(next.layers, 1, configLayer{source: Source{Kind: "user-settings", Location: path}, values: make(map[string]any)})
		user = 1
	}
	next.layers[user].values = maps.Clone(next.layers[user].values)
	for _, change := range patch.Changes {
		key := string(change.ID)
		if change.Unset {
			delete(next.layers[user].values, key)
			delete(next.runtime, key)
		} else {
			next.layers[user].values[key] = change.Value.raw()
			next.runtime[key] = change.Value.raw()
		}
	}
	return next
}

// WithPatch derives a candidate exclusively from frozen startup inputs and
// this instance's successful changes. Callers publish it only after saving.
func (c Config) WithPatch(patch SettingsPatch) (Config, error) {
	if err := patch.validate(); err != nil {
		return Config{}, err
	}
	layers := c.frozen().changed(patch, c.Paths.GlobalSettings)
	values := layers.resolved()
	// Credentials are managed by dedicated auth operations and may have been
	// replaced on this snapshot since startup. Never restore an old key when
	// changing an unrelated preference.
	credentials, err := settingsValues(c.settings())
	if err != nil {
		return Config{}, err
	}
	for key := range EnvironmentVariables() {
		if strings.HasSuffix(key, "_api_key") {
			delete(values, key)
			if value, ok := credentials[key]; ok {
				values[key] = value
			}
		}
	}
	next, err := decodeSettingMap(values)
	if err != nil {
		return Config{}, fmt.Errorf("config: candidate (including inherited values): %w", err)
	}
	next.Paths, next.Diagnostics, next.CodexCredentials = c.Paths, slices.Clone(c.Diagnostics), c.CodexCredentials
	next.ClaudeSubscriptionCredentials = c.ClaudeSubscriptionCredentials
	next.startupOverrides = c.startupOverrides
	next.Web, next.webCredentials, next.webEnv = c.Web.Clone(), c.webCredentials, c.webEnv
	next.layers = layers
	next.webUser = c.webUser
	return next, nil
}

// SettingState returns effective, known saved and inherited preference values.
// It never accepts a credential key or rereads external state.
type SettingState struct {
	Value            SettingValue
	Source           Source
	Saved            *SettingValue
	Inherited        *SettingValue
	InheritanceError string
}

func (c Config) SettingState(id Setting) (SettingState, error) {
	def, ok := settingDefinition(id)
	if !ok {
		return SettingState{}, fmt.Errorf("config: unsupported setting %q", id)
	}
	f := c.frozen()
	state := SettingState{}
	key := string(id)
	for _, layer := range f.layers {
		if _, ok := layer.values[key]; ok {
			state.Source = layer.source
			if layer.source.Kind == "env" {
				state.Source.Location = def.Environment
			}
			if layer.source.Kind == "user-settings" {
				value, err := valueFromRaw(def, layer.values[key])
				if err == nil {
					state.Saved = &value
				}
			}
		}
	}
	if _, ok := f.runtime[key]; ok {
		state.Source = Source{Kind: "runtime"}
	}
	value, err := valueFromRaw(def, f.resolved()[key])
	if err != nil {
		return SettingState{}, err
	}
	state.Value = value
	inherited, err := c.WithPatch(SettingsPatch{Changes: []SettingChange{{ID: id, Unset: true}}})
	if err != nil {
		state.InheritanceError = err.Error()
	} else {
		value, err := valueFromRaw(def, inherited.frozen().resolved()[key])
		if err != nil {
			state.InheritanceError = err.Error()
		} else {
			state.Inherited = &value
		}
	}
	return state, nil
}

func valueFromRaw(def SettingDefinition, raw any) (SettingValue, error) {
	if raw == nil {
		return def.Default, nil
	}
	// Reuse the startup decoder for coercion and validation; invalid lower
	// layers remain visible as an inheritance error instead of a fake default.
	c, err := decodeSettingMap(map[string]any{string(def.ID): raw})
	if err != nil {
		return SettingValue{}, err
	}
	if def.Kind == ContextWindowsValue {
		return SettingValue{Kind: def.Kind, Windows: c.settings().ContextWindows}, nil
	}
	values, err := settingsValues(c.settings())
	if err != nil {
		return SettingValue{}, err
	}
	normalized, ok := values[string(def.ID)]
	if !ok {
		return SettingValue{Kind: def.Kind}, nil
	}
	return ParseSettingValue(def.ID, fmt.Sprint(normalized))
}

// CredentialSource exposes only where an effective key came from, never its
// value. OAuth stores use their dedicated path functions instead.
func (c Config) CredentialSource(key string) Source {
	environment, ok := EnvironmentVariables()[key]
	if !ok || !strings.HasSuffix(key, "_api_key") {
		return Source{Kind: "unavailable"}
	}
	values, _ := settingsValues(c.settings())
	current, _ := values[key].(string)
	if current == "" {
		return Source{Kind: "not configured"}
	}
	frozen := c.frozen()
	source := Source{Kind: "user-auth", Location: c.Paths.GlobalAuth}
	for _, layer := range frozen.layers {
		if _, ok := layer.values[key]; ok {
			source = layer.source
			if source.Kind == "env" {
				source.Location = environment
			}
		}
	}
	old, _ := frozen.resolved()[key].(string)
	if old != current {
		source = Source{Kind: "runtime", Location: c.Paths.GlobalAuth}
	}
	return source
}
