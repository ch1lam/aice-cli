package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/web/exa"
)

func webValue(w config.WebConfig, id config.WebSetting) interaction.SettingValue {
	v := interaction.SettingValue{}
	switch id {
	case config.WebSearchEnabled:
		v.Kind, v.Bool = interaction.SettingBool, w.SearchEnabled
	case config.WebFetchEnabled:
		v.Kind, v.Bool = interaction.SettingBool, w.FetchEnabled
	case config.WebSearchMaxResults:
		v.Kind, v.Int = interaction.SettingInt, int64(w.DefaultMaxResults)
	case config.WebSearchTimeout:
		v.Kind, v.Duration = interaction.SettingDuration, w.SearchTimeout
	case config.WebFetchTimeout:
		v.Kind, v.Duration = interaction.SettingDuration, w.FetchTimeout
	case config.WebSearchPriority:
		v.Kind, v.List = interaction.SettingList, append([]string{}, w.Priority...)
	case config.WebAllowedDomains:
		v.Kind, v.List = interaction.SettingList, append([]string{}, w.AllowedDomains...)
	case config.WebExcludedDomains:
		v.Kind, v.List = interaction.SettingList, append([]string{}, w.ExcludedDomains...)
	}
	return v
}

func webSettingFields(c config.Config, disabled string) []interaction.SettingField {
	var fields []interaction.SettingField
	for _, d := range []struct {
		id                 config.WebSetting
		label, description string
	}{
		{config.WebSearchEnabled, "Web search", "Enable search for the next response."},
		{config.WebFetchEnabled, "Web fetch", "Enable direct public web page retrieval."},
		{config.WebSearchMaxResults, "Search result count", "Positive default result count."},
		{config.WebSearchTimeout, "Search timeout", "Positive duration, for example 25s."},
		{config.WebFetchTimeout, "Fetch timeout", "Positive duration, for example 30s."},
		{config.WebSearchPriority, "Search priority", "Ordered source list: native or service:<instance>. Empty permits no sources."},
		{config.WebAllowedDomains, "Allowed search domains", "One domain per entry, without scheme or path. Mutually exclusive with excluded domains."},
		{config.WebExcludedDomains, "Excluded search domains", "One domain per entry, without scheme or path. Mutually exclusive with allowed domains."},
	} {
		value := webValue(c.Web, d.id)
		inherited, err := c.WithWebPatch(config.WebPatch{Unset: []config.WebSetting{d.id}})
		field := interaction.SettingField{ID: "web." + string(d.id), Category: "tools", Label: d.label, Description: d.description, Kind: value.Kind, Value: value, DisabledReason: disabled, Applies: interaction.SettingNextRun, Source: interaction.SettingSource{Kind: "user-settings", Location: c.Paths.GlobalSettings}}
		if len(c.Web.ProjectRestricted) > 0 {
			field.Description += " Project restrictions: " + strings.Join(c.Web.ProjectRestricted, ", ")
		}
		if err == nil {
			v := webValue(inherited.Web, d.id)
			field.Inherited = &v
		} else {
			field.InheritanceError = err.Error()
		}
		defaults, _ := (config.Config{}).WithWeb(config.WebSettings{})
		def := webValue(defaults.Web, d.id)
		field.Default = &def
		// Project restrictions affect the effective value; the draft starts from
		// this instance's known user preference, never the tightened project layer.
		user, _ := (config.Config{}).WithWeb(c.WebPreferences())
		saved := webValue(user.Web, d.id)
		if c.HasWebPreference(d.id) {
			field.Saved = &saved
		} else {
			field.Source = interaction.SettingSource{Kind: "default"}
		}
		field.Value = saved
		for _, restriction := range c.Web.ProjectRestricted {
			if restriction == field.ID+"=false" {
				field.Effective = "Off (restricted by trusted project)"
				field.Source = interaction.SettingSource{Kind: "project", Location: c.Paths.ProjectSettings}
			}
		}
		fields = append(fields, field)
	}
	for _, id := range c.Web.ServiceIDs() {
		service := c.Web.Services[id]
		options, err := exa.ParseOptions(service.Options)
		description := service.Provider + " / " + service.API + " · " + service.CredentialDescription()
		if err != nil {
			description += " · " + err.Error()
		}
		for _, d := range []struct {
			key, label string
			value      interaction.SettingValue
		}{
			{"enabled", "Enabled", interaction.SettingValue{Kind: interaction.SettingBool, Bool: service.Enabled}},
			{"base_url", "Endpoint", interaction.SettingValue{Kind: interaction.SettingString, Text: service.BaseURL}},
			{"credential_env", "Credential environment variable", interaction.SettingValue{Kind: interaction.SettingString, Text: service.Credential.Env}},
			{"credential_auth_ref", "Stored credential reference", interaction.SettingValue{Kind: interaction.SettingString, Text: service.Credential.AuthRef}},
			{"type", "Search type", interaction.SettingValue{Kind: interaction.SettingEnum, Text: options.Type}},
		} {
			field := interaction.SettingField{ID: "web.service." + id + "." + d.key, Category: "tools", Label: id + " · " + d.label, Description: description, Kind: d.value.Kind, Value: d.value, DisabledReason: disabled, Applies: interaction.SettingNextRun, Source: interaction.SettingSource{Kind: "user-settings", Location: c.Paths.GlobalSettings}}
			if d.key == "type" {
				field.Choices = []interaction.SettingChoice{{Value: exa.SearchTypeAuto, Label: "Auto"}, {Value: exa.SearchTypeFast, Label: "Fast"}}
			}
			fields = append(fields, field)
		}
	}
	return fields
}

func webRequestPatch(c config.Config, request interaction.SettingsRequest) (config.WebPatch, error) {
	patch := config.WebPatch{}
	seen := map[string]bool{}
	for _, change := range request.Changes {
		if seen[change.ID] {
			return patch, fmt.Errorf("duplicate setting %s", change.ID)
		}
		seen[change.ID] = true
		v := change.Value
		if err := v.Validate(); err != nil && !change.Unset {
			return patch, err
		}
		id := config.WebSetting(strings.TrimPrefix(change.ID, "web."))
		if change.Unset {
			if v.Kind != "" || v.Text != "" || v.Bool || v.Int != 0 || v.Duration != 0 || v.Contexts != nil || v.List != nil {
				return patch, fmt.Errorf("unset cannot carry a value")
			}
			patch.Unset = append(patch.Unset, id)
			continue
		}
		expected := webValue(c.Web, id).Kind
		if strings.HasPrefix(change.ID, "web.service.") {
			key := strings.TrimPrefix(change.ID, "web.service.")
			index := strings.LastIndexByte(key, '.')
			if index < 0 {
				return patch, fmt.Errorf("invalid service field")
			}
			instance, field := key[:index], key[index+1:]
			service, ok := c.WebPreferences().Services[instance]
			if !ok {
				return patch, fmt.Errorf("unknown service %s", instance)
			}
			if patch.Services == nil {
				patch.Services = map[string]*config.WebServiceSettings{}
			}
			if patch.ServiceEdits == nil {
				patch.ServiceEdits = map[string]config.WebServicePatch{}
			}
			edit := patch.ServiceEdits[instance]
			if previous := patch.Services[instance]; previous != nil {
				service = *previous
			}
			switch field {
			case "enabled":
				expected = interaction.SettingBool
				service.Enabled = &v.Bool
				edit.Enabled = &v.Bool
			case "base_url":
				expected = interaction.SettingString
				service.BaseURL = v.Text
				edit.BaseURL = &v.Text
			case "credential_env":
				expected = interaction.SettingString
				service.Credential = config.WebCredentialRef{Env: strings.TrimSpace(v.Text)}
				edit.Credential = &service.Credential
			case "credential_auth_ref":
				expected = interaction.SettingString
				service.Credential = config.WebCredentialRef{AuthRef: strings.TrimSpace(v.Text)}
				edit.Credential = &service.Credential
			case "type":
				expected = interaction.SettingEnum
				raw := map[string]json.RawMessage{}
				if len(service.Options) > 0 {
					if err := json.Unmarshal(service.Options, &raw); err != nil {
						return patch, err
					}
				}
				raw["type"], _ = json.Marshal(v.Text)
				edit.Options = map[string]json.RawMessage{"type": raw["type"]}
				service.Options, _ = json.Marshal(raw)
			default:
				return patch, fmt.Errorf("unsupported service field %s", field)
			}
			if _, err := exa.ResolveBaseURL(service.BaseURL); err != nil {
				return patch, err
			}
			if _, err := exa.ParseOptions(service.Options); err != nil {
				return patch, err
			}
			patch.Services[instance] = &service
			patch.ServiceEdits[instance] = edit
		} else {
			switch id {
			case config.WebSearchEnabled:
				patch.SearchEnabled = &v.Bool
			case config.WebFetchEnabled:
				patch.FetchEnabled = &v.Bool
			case config.WebSearchMaxResults:
				n := int(v.Int)
				if int64(n) != v.Int {
					return patch, fmt.Errorf("result count too large")
				}
				patch.DefaultMaxResults = &n
			case config.WebSearchTimeout:
				if v.Duration <= 0 {
					return patch, fmt.Errorf("search timeout must be positive")
				}
				patch.SearchTimeout = &v.Duration
			case config.WebFetchTimeout:
				if v.Duration <= 0 {
					return patch, fmt.Errorf("fetch timeout must be positive")
				}
				patch.FetchTimeout = &v.Duration
			case config.WebSearchPriority:
				patch.Priority = &v.List
			case config.WebAllowedDomains:
				patch.AllowedDomains = &v.List
			case config.WebExcludedDomains:
				patch.ExcludedDomains = &v.List
			default:
				return patch, fmt.Errorf("unsupported web field %s", id)
			}
		}
		if v.Kind != expected {
			return patch, fmt.Errorf("%s requires %s", change.ID, expected)
		}
	}
	patch.Services = nil // existing services use field patches, never full replacement
	next, err := patch.Apply(c.WebPreferences())
	if err != nil {
		return patch, err
	}
	if next.Search != nil && len(next.Search.AllowedDomains) > 0 && len(next.Search.ExcludedDomains) > 0 {
		return patch, fmt.Errorf("allowed and excluded domains are mutually exclusive; clear the other list first")
	}
	return patch, nil
}

func (s *interactiveSession) applyWebRequest(ctx context.Context, request interaction.SettingsRequest) (result interaction.SettingsResult, err error) {
	current := s.settingsSnapshot().configuration
	patch, err := webRequestPatch(current, request)
	if err != nil {
		return result, err
	}
	candidate, err := current.WithWebPatch(patch)
	if err != nil {
		return result, err
	}
	if s.application == nil {
		return result, fmt.Errorf("application is required")
	}
	prepared, err := s.prepareWebSettings(candidate)
	if err != nil {
		return result, err
	}
	commit, err := config.SaveWebPatch(ctx, current.Paths, patch)
	result.Committed = commit.Committed
	result.Applies = interaction.SettingNextRun
	if err != nil {
		prepared.state.closeBackend()
		return result, err
	}
	s.publishWebSettings(prepared)
	result.Applied = true
	if commit.CleanupWarning != nil {
		result.Warnings = append(result.Warnings, commit.CleanupWarning.Error())
	}
	return result, nil
}
