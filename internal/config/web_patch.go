package config

import (
	"encoding/json"
	"fmt"
	"slices"
)

// WebSetting identifies a supported scalar or list in the web domain.
type WebSetting string

const (
	WebSearchEnabled    WebSetting = "search.enabled"
	WebSearchPriority   WebSetting = "search.priority"
	WebSearchMaxResults WebSetting = "search.default_max_results"
	WebSearchTimeout    WebSetting = "search.timeout"
	WebAllowedDomains   WebSetting = "search.allowed_domains"
	WebExcludedDomains  WebSetting = "search.excluded_domains"
	WebFetchEnabled     WebSetting = "fetch.enabled"
	WebFetchTimeout     WebSetting = "fetch.timeout"
)

func cloneWebSettings(settings WebSettings) WebSettings {
	result := settings
	if settings.Search != nil {
		v := *settings.Search
		result.Search = &v
		if v.Enabled != nil {
			b := *v.Enabled
			v.Enabled = &b
		}
		if v.Priority != nil {
			p := append([]string{}, (*v.Priority)...)
			v.Priority = &p
		}
		v.AllowedDomains = slices.Clone(v.AllowedDomains)
		v.ExcludedDomains = slices.Clone(v.ExcludedDomains)
	}
	if settings.Fetch != nil {
		v := *settings.Fetch
		result.Fetch = &v
		if v.Enabled != nil {
			b := *v.Enabled
			v.Enabled = &b
		}
	}
	if settings.Services != nil {
		result.Services = make(map[string]WebServiceSettings, len(settings.Services))
		for id, v := range settings.Services {
			v.Options = slices.Clone(v.Options)
			if v.Enabled != nil {
				b := *v.Enabled
				v.Enabled = &b
			}
			result.Services[id] = v
		}
	}
	return result
}

// WebPreferences is the known user layer before project tightening. It is a
// copy of startup/latest local preferences, never a live read of the file.
func (c Config) WebPreferences() WebSettings {
	if c.webUser != nil {
		return cloneWebSettings(*c.webUser)
	}
	// Support explicitly assembled Config snapshots at application/test seams.
	w := c.Web
	search, fetch := w.SearchEnabled, w.FetchEnabled
	priority := append([]string{}, w.Priority...)
	settings := WebSettings{
		Search:   &WebSearchSettings{Enabled: &search, Priority: &priority, DefaultMaxResults: w.DefaultMaxResults, AllowedDomains: w.AllowedDomains, ExcludedDomains: w.ExcludedDomains},
		Fetch:    &WebFetchSettings{Enabled: &fetch},
		Services: make(map[string]WebServiceSettings),
	}
	if w.SearchTimeout > 0 {
		settings.Search.Timeout = w.SearchTimeout.String()
	}
	if w.FetchTimeout > 0 {
		settings.Fetch.Timeout = w.FetchTimeout.String()
	}
	for id, service := range w.Services {
		enabled := service.Enabled
		settings.Services[id] = WebServiceSettings{Provider: service.Provider, API: service.API, BaseURL: service.BaseURL, Credential: service.Credential, Options: service.Options, Enabled: &enabled}
	}
	return cloneWebSettings(settings)
}

// Apply validates a candidate without reading files or credentials.
func (p WebPatch) Apply(settings WebSettings) (WebSettings, error) {
	if p.DefaultMaxResults != nil && *p.DefaultMaxResults <= 0 {
		return WebSettings{}, fmt.Errorf("config: web.search.default_max_results must be positive")
	}
	for id, edit := range p.ServiceEdits {
		for key, value := range edit.Options {
			if !json.Valid(value) {
				return WebSettings{}, fmt.Errorf("service %s option %s is invalid JSON", id, key)
			}
		}
		if _, ok := settings.Services[id]; !ok {
			return WebSettings{}, fmt.Errorf("service %s no longer exists; refresh before saving", id)
		}
	}
	next := p.apply(settings)
	for _, key := range p.Unset {
		switch key {
		case WebSearchEnabled:
			if next.Search != nil {
				next.Search.Enabled = nil
			}
		case WebSearchPriority:
			if next.Search != nil {
				next.Search.Priority = nil
			}
		case WebSearchMaxResults:
			if next.Search != nil {
				next.Search.DefaultMaxResults = 0
			}
		case WebSearchTimeout:
			if next.Search != nil {
				next.Search.Timeout = ""
			}
		case WebAllowedDomains:
			if next.Search != nil {
				next.Search.AllowedDomains = nil
			}
		case WebExcludedDomains:
			if next.Search != nil {
				next.Search.ExcludedDomains = nil
			}
		case WebFetchEnabled:
			if next.Fetch != nil {
				next.Fetch.Enabled = nil
			}
		case WebFetchTimeout:
			if next.Fetch != nil {
				next.Fetch.Timeout = ""
			}
		default:
			return WebSettings{}, fmt.Errorf("config: unsupported web setting %q", key)
		}
	}
	if err := next.Validate(); err != nil {
		return WebSettings{}, err
	}
	return next, nil
}

// WithWebPatch derives only this instance's domain changes; unrelated peer
// edits retained by the locked writer are not imported into the process.
func (c Config) WithWebPatch(patch WebPatch) (Config, error) {
	next, err := patch.Apply(c.WebPreferences())
	if err != nil {
		return Config{}, err
	}
	return c.WithWeb(next)
}

// WebServicePatch changes only named instance fields, preserving peer edits.
type WebServicePatch struct {
	Enabled    *bool
	BaseURL    *string
	Credential *WebCredentialRef
	Options    map[string]json.RawMessage
}

// MarshalJSON retains explicitly empty domain arrays while omitting absence.
func (s WebSearchSettings) MarshalJSON() ([]byte, error) {
	type plain WebSearchSettings
	data, err := json.Marshal(plain(s))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if s.AllowedDomains != nil {
		fields["allowed_domains"], err = json.Marshal(s.AllowedDomains)
		if err != nil {
			return nil, err
		}
	}
	if s.ExcludedDomains != nil {
		fields["excluded_domains"], err = json.Marshal(s.ExcludedDomains)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields)
}

func (c Config) HasWebPreference(id WebSetting) bool {
	p := c.WebPreferences()
	switch id {
	case WebSearchEnabled:
		return p.Search != nil && p.Search.Enabled != nil
	case WebSearchPriority:
		return p.Search != nil && p.Search.Priority != nil
	case WebSearchMaxResults:
		return p.Search != nil && p.Search.DefaultMaxResults != 0
	case WebSearchTimeout:
		return p.Search != nil && p.Search.Timeout != ""
	case WebAllowedDomains:
		return p.Search != nil && p.Search.AllowedDomains != nil
	case WebExcludedDomains:
		return p.Search != nil && p.Search.ExcludedDomains != nil
	case WebFetchEnabled:
		return p.Fetch != nil && p.Fetch.Enabled != nil
	case WebFetchTimeout:
		return p.Fetch != nil && p.Fetch.Timeout != ""
	}
	return false
}
