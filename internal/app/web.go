package app

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/web"
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

// webBackends is the fixed, read-only factory list assembled by the
// application. Adding a provider means adding an adapter and one entry here.
type webBackends struct {
	search     map[string]webSearchFactory
	newFetcher func(config.WebConfig) (web.FetchBackend, error)
}

func defaultWebBackends() webBackends {
	return webBackends{
		search: map[string]webSearchFactory{
			exa.ProviderID: newExaService,
		},
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

// webState is the frozen web wiring of one run environment: the tools that
// were registered, the resolver outcome for display, and the fingerprint the
// Guard uses for web_search approvals.
type webState struct {
	settings     config.WebConfig
	binding      web.Binding
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
			if candidate.APIID == "" {
				candidate.APIID = exa.APIID
			}
			services[id] = constructed
			switch {
			case !service.Enabled:
				candidate.Availability, candidate.Detail = web.AvailabilityDisabled, "instance disabled (enabled=false)"
			case !service.SecretPresent:
				candidate.Availability, candidate.Detail = web.AvailabilityMissingCredentials, "credential from "+service.CredentialDescription()+" is not set"
			default:
				candidate.Availability = web.AvailabilityReady
			}
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
	if err == nil && binding.Kind == web.BindingService {
		selected := services[binding.Selected.InstanceID]
		searchTool, toolErr := tool.NewWebSearch(tool.WebSearchOptions{
			Backend:           selected.backend,
			Label:             selected.label,
			Policy:            web.DomainPolicy{Allowed: settings.AllowedDomains, Excluded: settings.ExcludedDomains},
			DefaultMaxResults: settings.DefaultMaxResults,
		})
		if toolErr != nil {
			state.configErr = toolErr
			state.binding = web.Binding{Kind: web.BindingNone, Reason: toolErr.Error()}
		} else {
			state.searchTool = searchTool
			state.searchTarget = guard.SearchTargetFingerprint(binding.Selected.InstanceID, selected.origin)
			state.close = selected.close
		}
	}
	for id, service := range services {
		if state.searchTool != nil && id == binding.Selected.InstanceID {
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

func (s webState) closeBackend() {
	if s.close != nil {
		s.close()
	}
}

// webStatusLines renders the effective web configuration for /web and /settings.
func (s webState) statusLines() []string {
	lines := []string{"Web search: " + onOff(s.settings.SearchEnabled)}
	if len(s.settings.Priority) == 0 {
		lines = append(lines, "Priority: [] (no search source allowed)")
	} else {
		lines = append(lines, "Priority: "+strings.Join(s.settings.Priority, ", "))
	}
	switch {
	case s.configErr != nil:
		lines = append(lines, "Search source: unavailable — "+s.configErr.Error())
	case s.binding.Kind == web.BindingService:
		lines = append(lines, fmt.Sprintf("Search source: %s (%s, %s)", s.binding.Selected.Entry, s.binding.Selected.ProviderID, s.binding.Selected.EndpointOrigin))
	default:
		lines = append(lines, "Search source: none — "+s.binding.Reason)
	}
	for _, skipped := range s.binding.Skipped {
		lines = append(lines, fmt.Sprintf("  skipped %s: %s (%s)", skipped.Entry, skipped.Availability, skipped.Detail))
	}
	for _, candidate := range s.candidates {
		if candidate.Entry == web.PriorityNative {
			lines = append(lines, "  native: "+string(candidate.Availability)+" — "+candidate.Detail)
			continue
		}
		detail := string(candidate.Availability)
		if candidate.Detail != "" {
			detail += " — " + candidate.Detail
		}
		service := s.settings.Services[candidate.InstanceID]
		lines = append(lines, fmt.Sprintf("  %s: %s, %s, credential %s: %s", candidate.Entry, candidate.ProviderID, candidate.EndpointOrigin, service.CredentialDescription(), detail))
	}
	if len(s.settings.AllowedDomains) > 0 {
		lines = append(lines, "Allowed domains (policy): "+strings.Join(s.settings.AllowedDomains, ", "))
	}
	if len(s.settings.ExcludedDomains) > 0 {
		lines = append(lines, "Excluded domains (policy): "+strings.Join(s.settings.ExcludedDomains, ", "))
	}
	fetch := "Web fetch: " + onOff(s.settings.FetchEnabled)
	if s.settings.FetchEnabled {
		fetch += fmt.Sprintf(" (standard proxy environment, no authentication, HTML/text/Markdown only, %s timeout)", s.settings.FetchTimeout)
	}
	if s.fetchErr != nil {
		fetch += " — unavailable: " + s.fetchErr.Error()
	}
	lines = append(lines, fetch)
	for _, restriction := range s.settings.ProjectRestricted {
		lines = append(lines, "Project settings tightened: "+restriction)
	}
	return lines
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
