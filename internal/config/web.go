package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Web configuration keys. The "web" object and the "web_services" credential
// namespace are handled outside Viper: they are nested objects that must be
// replaced as a whole per layer and may only come from user-controlled files.
const (
	webSettingsKey    = "web"
	webCredentialsKey = "web_services"
	// WebAuthRefPrefix is the only accepted auth_ref form: web_services.<id>.
	WebAuthRefPrefix = webCredentialsKey + "."

	DefaultWebSearchTimeout     = 25 * time.Second
	DefaultWebFetchTimeout      = 30 * time.Second
	DefaultWebSearchMaxResults  = web.DefaultMaxResults
	maxWebServiceOptionsBytes   = 4 * 1024
	webSearchPriorityNativeOnly = web.PriorityNative
)

// WebSettings is the file schema of the "web" object in settings.json.
// Pointers distinguish "absent, use default" from an explicit value.
type WebSettings struct {
	Search   *WebSearchSettings            `json:"search,omitempty"`
	Services map[string]WebServiceSettings `json:"services,omitempty"`
	Fetch    *WebFetchSettings             `json:"fetch,omitempty"`
}

// WebSearchSettings configures the web_search tool. A nil Priority uses the
// default ["native"]; an explicit empty array allows no search source.
type WebSearchSettings struct {
	Enabled           *bool     `json:"enabled,omitempty"`
	Priority          *[]string `json:"priority,omitempty"`
	DefaultMaxResults int       `json:"default_max_results,omitempty"`
	Timeout           string    `json:"timeout,omitempty"`
	AllowedDomains    []string  `json:"allowed_domains,omitempty"`
	ExcludedDomains   []string  `json:"excluded_domains,omitempty"`
}

// WebServiceSettings configures one independent search service instance.
type WebServiceSettings struct {
	Provider   string           `json:"provider"`
	API        string           `json:"api,omitempty"`
	BaseURL    string           `json:"base_url,omitempty"`
	Credential WebCredentialRef `json:"credential"`
	Options    json.RawMessage  `json:"options,omitempty"`
	Enabled    *bool            `json:"enabled,omitempty"`
}

// WebCredentialRef references a secret without storing it in settings. Exactly
// one of Env (an environment variable name) or AuthRef (web_services.<id> in
// the auth file) must be set.
type WebCredentialRef struct {
	Env     string `json:"env,omitempty"`
	AuthRef string `json:"auth_ref,omitempty"`
}

// WebFetchSettings configures the web_fetch tool.
type WebFetchSettings struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

// WebConfig is the effective, defaulted web configuration inside a Config
// snapshot. Secrets are resolved once at load time from the referenced source.
type WebConfig struct {
	SearchEnabled     bool
	Priority          []string
	DefaultMaxResults int
	SearchTimeout     time.Duration
	AllowedDomains    []string
	ExcludedDomains   []string
	Services          map[string]WebService
	FetchEnabled      bool
	FetchTimeout      time.Duration
	// ProjectRestricted lists project-level tightening that applied.
	ProjectRestricted []string
}

// WebService is one effective service instance. Secret is empty when the
// referenced credential is absent; SecretPresent distinguishes that case.
type WebService struct {
	ID            string
	Provider      string
	API           string
	BaseURL       string
	Credential    WebCredentialRef
	Options       json.RawMessage
	Enabled       bool
	Secret        string
	SecretPresent bool
}

// Clone returns an independent copy so runs can freeze their snapshot.
func (w WebConfig) Clone() WebConfig {
	cloned := w
	cloned.Priority = slices.Clone(w.Priority)
	cloned.AllowedDomains = slices.Clone(w.AllowedDomains)
	cloned.ExcludedDomains = slices.Clone(w.ExcludedDomains)
	cloned.ProjectRestricted = slices.Clone(w.ProjectRestricted)
	if w.Services != nil {
		cloned.Services = make(map[string]WebService, len(w.Services))
		for id, service := range w.Services {
			service.Options = slices.Clone(service.Options)
			cloned.Services[id] = service
		}
	}
	return cloned
}

// ServiceIDs returns the configured instance IDs in sorted order.
func (w WebConfig) ServiceIDs() []string {
	ids := make([]string, 0, len(w.Services))
	for id := range w.Services {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// webLayer carries the user and project "web" objects and the auth-file
// credential namespace out of the file loop.
type webLayer struct {
	user        json.RawMessage
	project     json.RawMessage
	credentials map[string]string
	diagnostics []string
}

// extractWebValues removes the web keys from one file's values before Viper
// sees them. Project files may not define services or credentials.
func (l *webLayer) extractWebValues(path string, values map[string]any, isProject bool) error {
	if raw, ok := values[webSettingsKey]; ok {
		delete(values, webSettingsKey)
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("config: encode web settings in %s: %w", path, err)
		}
		if isProject {
			l.project = encoded
		} else {
			l.user = encoded
		}
	}
	if raw, ok := values[webCredentialsKey]; ok {
		delete(values, webCredentialsKey)
		if isProject {
			l.diagnostics = append(l.diagnostics, fmt.Sprintf("Ignored %s in project settings %s: web credentials are user-global only", webCredentialsKey, path))
			return nil
		}
		object, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("config: %s in %s must be an object of instance id to key", webCredentialsKey, path)
		}
		if l.credentials == nil {
			l.credentials = make(map[string]string, len(object))
		}
		for id, value := range object {
			secret, ok := value.(string)
			if !ok {
				return fmt.Errorf("config: %s.%s in %s must be a string", webCredentialsKey, id, path)
			}
			l.credentials[id] = secret
		}
	}
	return nil
}

func decodeWebSettings(raw json.RawMessage) (WebSettings, error) {
	var settings WebSettings
	if len(raw) == 0 {
		return settings, nil
	}
	if err := jsonutil.DecodeStrict(raw, &settings); err != nil {
		return WebSettings{}, fmt.Errorf("config: web: %w", err)
	}
	return settings, nil
}

// projectWebTightening accepts only search.enabled=false and fetch.enabled=false
// from a project layer. Anything else is ignored with a diagnostic; project
// files must not add services, endpoints, credentials or priorities.
func projectWebTightening(raw json.RawMessage, path string) (searchOff, fetchOff bool, diagnostic string) {
	if len(raw) == 0 {
		return false, false, ""
	}
	reject := fmt.Sprintf("Ignored web settings in project %s: project settings may only set web.search.enabled=false or web.fetch.enabled=false", path)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false, false, reject
	}
	for key, value := range object {
		var section map[string]json.RawMessage
		if (key != "search" && key != "fetch") || json.Unmarshal(value, &section) != nil {
			return false, false, reject
		}
		for field, fieldValue := range section {
			var enabled bool
			if field != "enabled" || json.Unmarshal(fieldValue, &enabled) != nil || enabled {
				return false, false, reject
			}
			if key == "search" {
				searchOff = true
			} else {
				fetchOff = true
			}
		}
	}
	return searchOff, fetchOff, ""
}

// Validate checks the file schema independently of credential availability.
func (s WebSettings) Validate() error {
	if s.Search != nil {
		if err := s.Search.validate(); err != nil {
			return err
		}
	}
	if s.Fetch != nil && s.Fetch.Timeout != "" {
		if _, err := parseWebTimeout(s.Fetch.Timeout); err != nil {
			return fmt.Errorf("config: web.fetch.timeout: %w", err)
		}
	}
	for id, service := range s.Services {
		if err := web.ValidateInstanceID(id); err != nil {
			return fmt.Errorf("config: web.services: %w", err)
		}
		if err := service.validate(id); err != nil {
			return err
		}
	}
	if s.Search != nil && s.Search.Priority != nil {
		seen := make(map[string]struct{}, len(*s.Search.Priority))
		for index, entry := range *s.Search.Priority {
			if _, dup := seen[entry]; dup {
				return fmt.Errorf("config: web.search.priority[%d]: duplicate entry %q", index, entry)
			}
			seen[entry] = struct{}{}
			if entry == web.PriorityNative {
				continue
			}
			id, ok := strings.CutPrefix(entry, web.PriorityServicePrefix)
			if !ok {
				return fmt.Errorf("config: web.search.priority[%d]: %q must be %q or %q<id>", index, entry, web.PriorityNative, web.PriorityServicePrefix)
			}
			if _, exists := s.Services[id]; !exists {
				return fmt.Errorf("config: web.search.priority[%d]: service %q is not configured under web.services", index, id)
			}
		}
	}
	return nil
}

func (s WebSearchSettings) validate() error {
	if s.DefaultMaxResults != 0 && (s.DefaultMaxResults < web.MinResults || s.DefaultMaxResults > web.MaxResults) {
		return fmt.Errorf("config: web.search.default_max_results must be between %d and %d", web.MinResults, web.MaxResults)
	}
	if s.Timeout != "" {
		if _, err := parseWebTimeout(s.Timeout); err != nil {
			return fmt.Errorf("config: web.search.timeout: %w", err)
		}
	}
	for _, domain := range s.AllowedDomains {
		if _, err := web.NormalizeDomain(domain); err != nil {
			return fmt.Errorf("config: web.search.allowed_domains: %w", err)
		}
	}
	for _, domain := range s.ExcludedDomains {
		if _, err := web.NormalizeDomain(domain); err != nil {
			return fmt.Errorf("config: web.search.excluded_domains: %w", err)
		}
	}
	return nil
}

func (s WebServiceSettings) validate(id string) error {
	prefix := "config: web.services." + id
	if strings.TrimSpace(s.Provider) == "" || strings.ContainsAny(s.Provider, " \t\r\n") {
		return fmt.Errorf("%s.provider is required and must not contain whitespace", prefix)
	}
	if strings.ContainsAny(s.API, " \t\r\n") {
		return fmt.Errorf("%s.api must not contain whitespace", prefix)
	}
	if s.BaseURL != "" {
		if err := validateWebBaseURL(s.BaseURL); err != nil {
			return fmt.Errorf("%s.base_url: %w", prefix, err)
		}
	}
	if err := s.Credential.validate(id); err != nil {
		return fmt.Errorf("%s.credential: %w", prefix, err)
	}
	if len(s.Options) > 0 {
		if len(s.Options) > maxWebServiceOptionsBytes {
			return fmt.Errorf("%s.options exceeds %d bytes", prefix, maxWebServiceOptionsBytes)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(s.Options, &object); err != nil || object == nil {
			return fmt.Errorf("%s.options must be a JSON object", prefix)
		}
	}
	return nil
}

func (c WebCredentialRef) validate(id string) error {
	switch {
	case c.Env != "" && c.AuthRef != "":
		return errors.New("env and auth_ref are mutually exclusive")
	case c.Env == "" && c.AuthRef == "":
		return errors.New("env or auth_ref is required")
	case c.Env != "":
		if strings.ContainsAny(c.Env, " \t\r\n=") {
			return fmt.Errorf("env %q is not a valid variable name", c.Env)
		}
	default:
		ref, ok := strings.CutPrefix(c.AuthRef, WebAuthRefPrefix)
		if !ok || ref == "" {
			return fmt.Errorf("auth_ref %q must have the form %s<id>", c.AuthRef, WebAuthRefPrefix)
		}
		if err := web.ValidateInstanceID(ref); err != nil {
			return fmt.Errorf("auth_ref: %w", err)
		}
		if ref != id {
			return fmt.Errorf("auth_ref %q must reference this instance (%s%s)", c.AuthRef, WebAuthRefPrefix, id)
		}
	}
	return nil
}

// validateWebBaseURL requires an absolute http(s) origin without userinfo,
// fragment or query. HTTP is accepted only for loopback development gateways.
func validateWebBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("must not contain whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("must be an absolute http(s) URL with a host")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.RawQuery != "" || parsed.ForceQuery {
		return errors.New("must not contain userinfo, query or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return errors.New("http is only allowed for loopback development gateways; use https")
		}
	default:
		return errors.New("must use https (or http for loopback)")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

func parseWebTimeout(value string) (time.Duration, error) {
	timeout, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || timeout <= 0 {
		return 0, errors.New("must be a positive duration such as 25s")
	}
	return timeout, nil
}

// effectiveWeb derives the defaulted snapshot from the user layer, applies
// project tightening and resolves credential references. Env lookups happen
// only when the loader enabled environment input.
func effectiveWeb(layer webLayer, projectPath string, lookupEnv func(string) (string, bool)) (WebConfig, []string, error) {
	settings, err := decodeWebSettings(layer.user)
	if err != nil {
		return WebConfig{}, nil, err
	}
	if err := settings.Validate(); err != nil {
		return WebConfig{}, nil, err
	}
	effective := settings.effective(layer.credentials, lookupEnv)
	diagnostics := slices.Clone(layer.diagnostics)
	searchOff, fetchOff, diagnostic := projectWebTightening(layer.project, projectPath)
	if diagnostic != "" {
		diagnostics = append(diagnostics, diagnostic)
	}
	if searchOff {
		effective.SearchEnabled = false
		effective.ProjectRestricted = append(effective.ProjectRestricted, "web.search.enabled=false")
	}
	if fetchOff {
		effective.FetchEnabled = false
		effective.ProjectRestricted = append(effective.ProjectRestricted, "web.fetch.enabled=false")
	}
	return effective, diagnostics, nil
}

func (s WebSettings) effective(credentials map[string]string, lookupEnv func(string) (string, bool)) WebConfig {
	effective := WebConfig{
		SearchEnabled:     true,
		Priority:          []string{webSearchPriorityNativeOnly},
		DefaultMaxResults: DefaultWebSearchMaxResults,
		SearchTimeout:     DefaultWebSearchTimeout,
		FetchEnabled:      true,
		FetchTimeout:      DefaultWebFetchTimeout,
	}
	if s.Search != nil {
		if s.Search.Enabled != nil {
			effective.SearchEnabled = *s.Search.Enabled
		}
		if s.Search.Priority != nil {
			effective.Priority = slices.Clone(*s.Search.Priority)
			if effective.Priority == nil {
				effective.Priority = []string{}
			}
		}
		if s.Search.DefaultMaxResults != 0 {
			effective.DefaultMaxResults = s.Search.DefaultMaxResults
		}
		if s.Search.Timeout != "" {
			effective.SearchTimeout, _ = parseWebTimeout(s.Search.Timeout) // validated
		}
		effective.AllowedDomains = slices.Clone(s.Search.AllowedDomains)
		effective.ExcludedDomains = slices.Clone(s.Search.ExcludedDomains)
	}
	if s.Fetch != nil {
		if s.Fetch.Enabled != nil {
			effective.FetchEnabled = *s.Fetch.Enabled
		}
		if s.Fetch.Timeout != "" {
			effective.FetchTimeout, _ = parseWebTimeout(s.Fetch.Timeout) // validated
		}
	}
	if len(s.Services) > 0 {
		effective.Services = make(map[string]WebService, len(s.Services))
	}
	for id, service := range s.Services {
		instance := WebService{
			ID: id, Provider: service.Provider, API: service.API, BaseURL: strings.TrimSpace(service.BaseURL),
			Credential: service.Credential, Options: slices.Clone(service.Options), Enabled: true,
		}
		if service.Enabled != nil {
			instance.Enabled = *service.Enabled
		}
		switch {
		case service.Credential.Env != "":
			if lookupEnv != nil {
				if value, ok := lookupEnv(service.Credential.Env); ok && strings.TrimSpace(value) != "" {
					instance.Secret, instance.SecretPresent = strings.TrimSpace(value), true
				}
			}
		case service.Credential.AuthRef != "":
			if value, ok := credentials[strings.TrimPrefix(service.Credential.AuthRef, WebAuthRefPrefix)]; ok && value != "" {
				instance.Secret, instance.SecretPresent = value, true
			}
		}
		effective.Services[id] = instance
	}
	return effective
}

// CredentialDescription names the credential source without revealing it.
func (s WebService) CredentialDescription() string {
	if s.Credential.Env != "" {
		return "environment variable " + s.Credential.Env
	}
	return "auth store " + s.Credential.AuthRef
}

// WithWeb publishes a replacement web snapshot for this instance after a save.
// Credential references resolve against the same auth-store values and
// environment this snapshot was loaded with; no file is reread.
func (c Config) WithWeb(settings WebSettings) (Config, error) {
	if err := settings.Validate(); err != nil {
		return Config{}, err
	}
	next := c
	effective := settings.effective(c.webCredentials, c.webEnv)
	// Project tightening remains in force for this process.
	for _, restriction := range c.Web.ProjectRestricted {
		switch restriction {
		case "web.search.enabled=false":
			effective.SearchEnabled = false
		case "web.fetch.enabled=false":
			effective.FetchEnabled = false
		}
	}
	effective.ProjectRestricted = slices.Clone(c.Web.ProjectRestricted)
	next.Web = effective
	copy := cloneWebSettings(settings)
	next.webUser = &copy
	return next, nil
}

// WithWebCredential records a credential saved to the auth store during this
// process so the next WithWeb resolves it without rereading the file.
func (c Config) WithWebCredential(instanceID, secret string) Config {
	next := c
	next.webCredentials = make(map[string]string, len(c.webCredentials)+1)
	for id, value := range c.webCredentials {
		next.webCredentials[id] = value
	}
	if secret == "" {
		delete(next.webCredentials, instanceID)
	} else {
		next.webCredentials[instanceID] = secret
	}
	return next
}

// WebPatch is one user action on the "web" object. Nil pointers leave a field
// unchanged; a nil Services value removes that instance.
type WebPatch struct {
	ServiceEdits      map[string]WebServicePatch
	Services          map[string]*WebServiceSettings
	Priority          *[]string
	SearchEnabled     *bool
	FetchEnabled      *bool
	DefaultMaxResults *int
	SearchTimeout     *time.Duration
	FetchTimeout      *time.Duration
	AllowedDomains    *[]string
	ExcludedDomains   *[]string
	// Unset removes individual user fields, restoring their product defaults.
	Unset []WebSetting
}

// SaveWebSettingsFile applies one patch to the "web" object of the user
// settings file under the shared lock. Other keys and unrelated instances are
// preserved; the result is validated before it replaces the file.
func SaveWebSettingsFile(ctx context.Context, paths Paths, patch WebPatch) (WebSettings, error) {
	if err := paths.validate(); err != nil {
		return WebSettings{}, err
	}
	result, commit, err := saveWebPatch(ctx, paths, patch)
	return result, legacyCommitError(commit, err)
}

// SaveWebPatch exposes the replacement outcome separately from cleanup.
func SaveWebPatch(ctx context.Context, paths Paths, patch WebPatch) (CommitResult, error) {
	_, result, err := saveWebPatch(ctx, paths, patch)
	return result, err
}

func saveWebPatch(ctx context.Context, paths Paths, patch WebPatch) (WebSettings, CommitResult, error) {
	if err := paths.validate(); err != nil {
		return WebSettings{}, CommitResult{}, err
	}
	var result WebSettings
	commit, err := editFile(ctx, paths.GlobalSettings, func(values map[string]any) error {
		var raw json.RawMessage
		if existing, ok := values[webSettingsKey]; ok {
			encoded, err := json.Marshal(existing)
			if err != nil {
				return err
			}
			raw = encoded
		}
		settings, err := decodeWebSettings(raw)
		if err != nil {
			return fmt.Errorf("existing web settings left unchanged: %w", err)
		}
		if err := settings.Validate(); err != nil {
			return fmt.Errorf("existing web settings left unchanged: %w", err)
		}
		next, err := patch.Apply(settings)
		if err != nil {
			return err
		}
		if err := next.Validate(); err != nil {
			return err
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		var generic any
		if err := decodeValues(encoded, &generic); err != nil {
			return err
		}
		values[webSettingsKey] = generic
		result = next
		return nil
	})
	if err != nil {
		return WebSettings{}, commit, err
	}
	return result, commit, nil
}

func (p WebPatch) apply(settings WebSettings) WebSettings {
	next := cloneWebSettings(settings)
	if len(p.Services) > 0 {
		next.Services = make(map[string]WebServiceSettings, len(settings.Services)+len(p.Services))
		for id, service := range settings.Services {
			next.Services[id] = service
		}
		for id, service := range p.Services {
			if service == nil {
				delete(next.Services, id)
				continue
			}
			next.Services[id] = *service
		}
		if len(next.Services) == 0 {
			next.Services = nil
		}
	}
	for id, edit := range p.ServiceEdits {
		service, ok := next.Services[id]
		if !ok {
			continue
		}
		if edit.Enabled != nil {
			b := *edit.Enabled
			service.Enabled = &b
		}
		if edit.BaseURL != nil {
			service.BaseURL = *edit.BaseURL
		}
		if edit.Credential != nil {
			service.Credential = *edit.Credential
		}
		if len(edit.Options) > 0 {
			options := map[string]json.RawMessage{}
			_ = json.Unmarshal(service.Options, &options)
			for key, value := range edit.Options {
				options[key] = slices.Clone(value)
			}
			service.Options, _ = json.Marshal(options)
		}
		next.Services[id] = service
	}
	if p.Priority != nil || p.SearchEnabled != nil || p.DefaultMaxResults != nil || p.SearchTimeout != nil || p.AllowedDomains != nil || p.ExcludedDomains != nil {
		search := WebSearchSettings{}
		if settings.Search != nil {
			search = *settings.Search
		}
		if p.Priority != nil {
			priority := slices.Clone(*p.Priority)
			if priority == nil {
				priority = []string{}
			}
			search.Priority = &priority
		}
		if p.SearchEnabled != nil {
			enabled := *p.SearchEnabled
			search.Enabled = &enabled
		}
		if p.DefaultMaxResults != nil {
			search.DefaultMaxResults = *p.DefaultMaxResults
		}
		if p.SearchTimeout != nil {
			search.Timeout = p.SearchTimeout.String()
		}
		if p.AllowedDomains != nil {
			search.AllowedDomains = slices.Clone(*p.AllowedDomains)
		}
		if p.ExcludedDomains != nil {
			search.ExcludedDomains = slices.Clone(*p.ExcludedDomains)
		}
		next.Search = &search
	}
	if p.FetchEnabled != nil || p.FetchTimeout != nil {
		fetch := WebFetchSettings{}
		if settings.Fetch != nil {
			fetch = *settings.Fetch
		}
		if p.FetchEnabled != nil {
			enabled := *p.FetchEnabled
			fetch.Enabled = &enabled
		}
		if p.FetchTimeout != nil {
			fetch.Timeout = p.FetchTimeout.String()
		}
		next.Fetch = &fetch
	}
	return next
}

// SaveWebCredentialFile stores one service credential under web_services in
// the auth file. An empty secret removes the entry. Other credentials and
// provider keys are preserved.
func SaveWebCredentialFile(ctx context.Context, paths Paths, instanceID, secret string) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if err := web.ValidateInstanceID(instanceID); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if strings.ContainsAny(secret, "\r\n") {
		return errors.New("config: web service credential must be one line")
	}
	return patchFileWith(ctx, paths.GlobalAuth, func(values map[string]any) error {
		namespace := make(map[string]any)
		if existing, ok := values[webCredentialsKey]; ok {
			object, ok := existing.(map[string]any)
			if !ok {
				return fmt.Errorf("existing %s left unchanged: not an object", webCredentialsKey)
			}
			for id, value := range object {
				namespace[id] = value
			}
		}
		if secret == "" {
			delete(namespace, instanceID)
		} else {
			namespace[instanceID] = secret
		}
		if len(namespace) == 0 {
			delete(values, webCredentialsKey)
		} else {
			values[webCredentialsKey] = namespace
		}
		return nil
	})
}
