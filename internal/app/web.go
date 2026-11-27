package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/web"
	deepseekweb "github.com/ch1lam/aice-cli/internal/web/deepseek"
	"github.com/ch1lam/aice-cli/internal/web/exa"
	"github.com/ch1lam/aice-cli/internal/web/httpfetch"
)

// webSearchService is one constructed search backend plus its display facts.
type webSearchService struct {
	backend web.SearchBackend
	origin  string
	label   string
	close   func()
}

// webSearchFactory constructs a backend for one configured instance. It must
// validate configuration without network access; a missing credential is
// reported through the returned service, not as a construction failure.
type webSearchFactory func(service config.WebService, settings config.WebConfig) (webSearchService, error)

// webProvider describes one search service the application can construct
// and the /web flows can add. The list is fixed; there is no registry.
type webProvider struct {
	id, label, api, baseURL string
	// keyEnv is the environment variable /web offers as a credential source.
	keyEnv string
	// searchTypes are the choices of the provider's "type" option; nil means
	// the provider has none.
	searchTypes []interaction.SettingChoice
	// parseOptions validates instance options without network access.
	parseOptions func(json.RawMessage) error
	factory      webSearchFactory
}

var webProviders = []webProvider{
	{
		id: exa.ProviderID, label: exa.Label, api: exa.APIID, baseURL: exa.DefaultBaseURL, keyEnv: "EXA_API_KEY",
		searchTypes:  []interaction.SettingChoice{{Value: exa.SearchTypeAuto, Label: "Auto"}, {Value: exa.SearchTypeFast, Label: "Fast"}},
		parseOptions: func(raw json.RawMessage) error { _, err := exa.ParseOptions(raw); return err },
		factory:      newExaService,
	},
	{
		id: deepseekweb.ProviderID, label: deepseekweb.Label, api: deepseekweb.APIID, baseURL: deepseekweb.DefaultBaseURL, keyEnv: "DEEPSEEK_API_KEY",
		parseOptions: deepseekweb.ParseOptions,
		factory:      newDeepSeekService,
	},
}

func webProviderByID(id string) (webProvider, bool) {
	for _, provider := range webProviders {
		if provider.id == id {
			return provider, true
		}
	}
	return webProvider{}, false
}

// webBackends is the fixed, read-only factory list assembled by the
// application. Adding a provider means adding an adapter and one entry in
// webProviders.
type webBackends struct {
	search     map[string]webSearchFactory
	newFetcher func(config.WebConfig) (web.FetchBackend, error)
}

func defaultWebBackends() webBackends {
	search := make(map[string]webSearchFactory, len(webProviders))
	for _, provider := range webProviders {
		search[provider.id] = provider.factory
	}
	return webBackends{
		search: search,
		newFetcher: func(settings config.WebConfig) (web.FetchBackend, error) {
			return httpfetch.New(httpfetch.Config{Timeout: settings.FetchTimeout})
		},
	}
}

func newExaService(service config.WebService, settings config.WebConfig) (webSearchService, error) {
	if service.API != "" && service.API != exa.APIID {
		return webSearchService{}, fmt.Errorf("api %q is not supported by provider %q (use %q)", service.API, exa.ProviderID, exa.APIID)
	}
	client, err := exa.New(exa.Config{
		InstanceID: service.ID,
		BaseURL:    service.BaseURL,
		APIKey:     service.Secret,
		Options:    service.Options,
		Timeout:    settings.SearchTimeout,
	})
	if err != nil {
		return webSearchService{}, err
	}
	return webSearchService{backend: client, origin: client.Origin(), label: exa.Label + " / " + service.ID, close: client.Close}, nil
}

func newDeepSeekService(service config.WebService, settings config.WebConfig) (webSearchService, error) {
	if service.API != "" && service.API != deepseekweb.APIID {
		return webSearchService{}, fmt.Errorf("api %q is not supported by provider %q (use %q)", service.API, deepseekweb.ProviderID, deepseekweb.APIID)
	}
	client, err := deepseekweb.New(deepseekweb.Config{
		InstanceID: service.ID,
		BaseURL:    service.BaseURL,
		APIKey:     service.Secret,
		Options:    service.Options,
		Timeout:    settings.SearchTimeout,
	})
	if err != nil {
		return webSearchService{}, err
	}
	return webSearchService{backend: client, origin: client.Origin(), label: deepseekweb.Label + " / " + service.ID, close: client.Close}, nil
}

// webState is the frozen web wiring of one run environment: the tools that
// were registered, the provider-hosted search policy, the resolver outcome
// for display, and the fingerprint the Guard uses to tell whether web_search
// is bound.
type webState struct {
	settings config.WebConfig
	binding  web.Binding
	// hosted is the provider-hosted search policy under a native binding;
	// each request's model decides whether it applies.
	hosted       *llm.WebSearch
	candidates   []web.Candidate
	configErr    error
	searchTool   agent.Tool
	fetchTool    agent.Tool
	fetchErr     error
	searchTarget string
	close        func()
}

func (s webState) tools() []agent.Tool {
	var tools []agent.Tool
	if s.searchTool != nil {
		tools = append(tools, s.searchTool)
	}
	if s.fetchTool != nil {
		tools = append(tools, s.fetchTool)
	}
	return tools
}

// bindWeb resolves the search source and constructs the web tools for one
// run environment. Configuration mistakes disable web_search with a visible
// reason; they never fail startup or select another service silently.
func bindWeb(backends webBackends, settings config.WebConfig) webState {
	state := webState{settings: settings.Clone()}
	services := make(map[string]webSearchService)
	candidates := make(map[string]web.Candidate, len(settings.Services)+1)
	for _, id := range settings.ServiceIDs() {
		service := settings.Services[id]
		candidate := web.Candidate{Entry: web.ServiceEntry(id), InstanceID: id, ProviderID: service.Provider, APIID: service.API}
		factory, known := backends.search[service.Provider]
		switch {
		case !known:
			candidate.Availability, candidate.Detail = web.AvailabilityInvalidConfig, fmt.Sprintf("provider %q is not supported", service.Provider)
		default:
			constructed, err := factory(service, settings)
			if err != nil {
				candidate.Availability, candidate.Detail = web.AvailabilityInvalidConfig, err.Error()
				break
			}
			candidate.EndpointOrigin = constructed.origin
			if provider, ok := webProviderByID(service.Provider); ok && candidate.APIID == "" {
				candidate.APIID = provider.api
			}
			services[id] = constructed
			if service.SecretPresent {
				candidate.Availability = web.AvailabilityReady
			} else {
				candidate.Availability, candidate.Detail = web.AvailabilityMissingCredentials, "credential from "+service.CredentialDescription()+" is not set"
			}
		}
		// A disabled instance is skipped even when its definition is invalid,
		// so disabling a broken account lets later sources be considered.
		if !service.Enabled {
			candidate.Availability, candidate.Detail = web.AvailabilityDisabled, "instance disabled (enabled=false)"
		}
		candidates[candidate.Entry] = candidate
		state.candidates = append(state.candidates, candidate)
	}
	native := web.NativeCandidate()
	candidates[native.Entry] = native
	state.candidates = append([]web.Candidate{native}, state.candidates...)

	binding, err := web.Resolve(web.ResolveInput{Enabled: settings.SearchEnabled, Priority: settings.Priority, Candidates: candidates})
	state.configErr = err
	state.binding = binding
	bound := binding.Selected
	if binding.Kind == web.BindingNative {
		bound = binding.Fallback
		allowed, excluded, policyErr := web.CombineDomains(web.DomainPolicy{Allowed: settings.AllowedDomains, Excluded: settings.ExcludedDomains}, web.SearchRequest{})
		if policyErr != nil {
			state.configErr = policyErr
		} else {
			state.hosted = &llm.WebSearch{AllowedDomains: allowed, ExcludedDomains: excluded, Replaces: tool.WebSearchName}
		}
	}
	if state.configErr == nil && bound.InstanceID != "" {
		selected := services[bound.InstanceID]
		searchTool, toolErr := tool.NewWebSearch(tool.WebSearchOptions{
			Backend:           selected.backend,
			Label:             selected.label,
			Policy:            web.DomainPolicy{Allowed: settings.AllowedDomains, Excluded: settings.ExcludedDomains},
			DefaultMaxResults: settings.DefaultMaxResults,
		})
		if toolErr != nil {
			state.configErr = toolErr
			state.binding = web.Binding{Kind: web.BindingNone, Reason: toolErr.Error()}
			state.hosted = nil
		} else {
			state.searchTool = searchTool
			state.searchTarget = guard.SearchTargetFingerprint(bound.InstanceID, selected.origin)
			state.close = selected.close
		}
	}
	for id, service := range services {
		if state.searchTool != nil && id == bound.InstanceID {
			continue
		}
		if service.close != nil {
			service.close()
		}
	}
	if settings.FetchEnabled {
		fetcher, err := backends.newFetcher(settings)
		if err != nil {
			state.fetchErr = err
		} else if fetchTool, err := tool.NewWebFetch(fetcher); err != nil {
			state.fetchErr = err
		} else {
			state.fetchTool = fetchTool
		}
	}
	return state
}

// webSearch returns an independent copy of the hosted search policy, nil
// when no native binding applies.
func (s webState) webSearch() *llm.WebSearch {
	if s.hosted == nil {
		return nil
	}
	search := s.hosted.Clone()
	return &search
}

func (s webState) closeBackend() {
	if s.close != nil {
		s.close()
	}
}

// statusDetails describes the effective web configuration for /web and
// Settings: the chosen source first, then every candidate and policy.
func (s webState) statusDetails() []interaction.Detail {
	searchTone := interaction.ToneGood
	if !s.settings.SearchEnabled {
		searchTone = interaction.ToneMuted
	}
	source, sourceTone := s.sourceSummary()
	switch {
	case s.configErr != nil:
		source += " — " + s.configErr.Error()
	case s.binding.Kind == web.BindingService:
		source = fmt.Sprintf("%s (%s, %s)", s.binding.Selected.Entry, s.binding.Selected.ProviderID, s.binding.Selected.EndpointOrigin)
	case s.binding.Kind == web.BindingNative && s.binding.Fallback.Entry != "":
		fallback := s.binding.Fallback
		source = fmt.Sprintf("native for models that support it; otherwise %s (%s, %s)", fallback.Entry, fallback.ProviderID, fallback.EndpointOrigin)
	case s.binding.Kind == web.BindingNative:
		source = "native for models that support it; other models have no web_search — " + s.binding.Reason
	default:
		source += " — " + s.binding.Reason
	}
	priority := strings.Join(s.settings.Priority, ", ")
	if priority == "" {
		priority = "[] (no search source allowed)"
	}
	details := []interaction.Detail{
		{Key: "Web search", Value: onOff(s.settings.SearchEnabled), Tone: searchTone},
		{Key: "Search source", Value: source, Tone: sourceTone},
		{Key: "Priority", Value: priority},
	}
	var candidates []interaction.Detail
	skipped := make(map[string]bool)
	for _, entry := range s.binding.Skipped {
		skipped[entry.Entry] = true
		candidates = append(candidates, interaction.Detail{Indent: 2, Key: "skipped " + entry.Entry, Value: fmt.Sprintf("%s (%s)", entry.Availability, entry.Detail), Tone: interaction.ToneMuted})
	}
	for _, candidate := range s.candidates {
		if skipped[candidate.Entry] {
			continue
		}
		value := string(candidate.Availability)
		if candidate.Entry != web.PriorityNative {
			service := s.settings.Services[candidate.InstanceID]
			value = fmt.Sprintf("%s, %s, credential %s: %s", candidate.ProviderID, candidate.EndpointOrigin, service.CredentialDescription(), candidate.Availability)
		}
		if candidate.Detail != "" {
			value += " — " + candidate.Detail
		}
		candidates = append(candidates, interaction.Detail{Indent: 2, Key: candidate.Entry, Value: value, Tone: availabilityTone(candidate.Availability)})
	}
	details = append(details, candidates...)
	if len(s.settings.AllowedDomains) > 0 {
		details = append(details, interaction.Detail{Key: "Allowed domains (policy)", Value: strings.Join(s.settings.AllowedDomains, ", ")})
	}
	if len(s.settings.ExcludedDomains) > 0 {
		details = append(details, interaction.Detail{Key: "Excluded domains (policy)", Value: strings.Join(s.settings.ExcludedDomains, ", ")})
	}
	fetch, fetchTone := onOff(s.settings.FetchEnabled), interaction.ToneMuted
	if s.settings.FetchEnabled {
		fetch, fetchTone = fetch+fmt.Sprintf(" (standard proxy environment, no authentication, HTML/text/Markdown only, %s timeout)", s.settings.FetchTimeout), interaction.ToneGood
	}
	if s.fetchErr != nil {
		fetch, fetchTone = fetch+" — unavailable: "+s.fetchErr.Error(), interaction.ToneBad
	}
	details = append(details, interaction.Detail{Key: "Web fetch", Value: fetch, Tone: fetchTone})
	for _, restriction := range s.settings.ProjectRestricted {
		details = append(details, interaction.Detail{Key: "Project settings tightened", Value: restriction, Tone: interaction.ToneWarning})
	}
	return details
}

// sourceSummary is the short search source state a Settings row shows.
func (s webState) sourceSummary() (string, interaction.DetailTone) {
	switch {
	case s.configErr != nil:
		return "unavailable", interaction.ToneBad
	case !s.settings.SearchEnabled:
		return "search off", interaction.ToneMuted
	case s.binding.Kind == web.BindingService:
		return s.binding.Selected.Entry, interaction.ToneGood
	case s.binding.Kind == web.BindingNative && s.binding.Fallback.Entry != "":
		return "native, then " + s.binding.Fallback.Entry, interaction.ToneGood
	case s.binding.Kind == web.BindingNative:
		return "native", interaction.ToneGood
	}
	return "none", interaction.ToneWarning
}

func availabilityTone(availability web.Availability) interaction.DetailTone {
	switch availability {
	case web.AvailabilityReady:
		return interaction.ToneGood
	case web.AvailabilityMissingCredentials:
		return interaction.ToneWarning
	case web.AvailabilityInvalidConfig:
		return interaction.ToneBad
	}
	return interaction.ToneMuted
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
