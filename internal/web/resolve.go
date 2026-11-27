package web

import (
	"fmt"
	"regexp"
	"strings"
)

// PriorityNative is the priority entry for provider-hosted search: the model
// provider searches during its response when the request's model supports it.
const PriorityNative = "native"

// PriorityServicePrefix marks a configured service instance in the priority list.
const PriorityServicePrefix = "service:"

// InstanceIDPattern bounds instance identifiers so they are safe as JSON keys,
// credential references and display labels.
var InstanceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// BindingKind identifies which execution path a resolved source uses.
type BindingKind string

const (
	// BindingNone means no search source is usable in this run.
	BindingNone BindingKind = "none"
	// BindingService means a configured independent search service handles
	// local web_search calls.
	BindingService BindingKind = "service"
	// BindingNative means models with provider-hosted search use it; a
	// usable service after native in the priority list, if any, serves
	// web_search for models without it.
	BindingNative BindingKind = "native"
)

// Availability is the static state of one candidate. It never reflects network
// reachability, which would require probing a paid API.
type Availability string

const (
	AvailabilityReady              Availability = "ready"
	AvailabilityDisabled           Availability = "disabled"
	AvailabilityMissingCredentials Availability = "missing_credentials"
	AvailabilityInvalidConfig      Availability = "invalid_config"
)

// Candidate describes one configured search source for the resolver.
type Candidate struct {
	// Entry is the priority list entry: "native" or "service:<id>".
	Entry string
	// InstanceID is set for service entries.
	InstanceID string
	ProviderID string
	APIID      string
	// EndpointOrigin is the scheme://host[:port] the instance connects to.
	EndpointOrigin string
	// Availability is the caller-computed static state.
	Availability Availability
	// Detail explains a non-ready state in one line.
	Detail string
}

// Binding is the immutable outcome of one resolution for one run.
type Binding struct {
	Kind     BindingKind
	Selected Candidate
	// Fallback is the service that serves models without hosted search under
	// a native binding; its Entry is empty when there is none.
	Fallback Candidate
	// Skipped explains, in priority order, why earlier candidates were not used.
	Skipped []Diagnostic
	// Reason explains a BindingNone outcome.
	Reason string
}

// Diagnostic is one per-candidate explanation for display.
type Diagnostic struct {
	Entry        string
	Availability Availability
	Detail       string
}

// ResolveInput is everything the pure resolver needs. It must not read files,
// the environment or the network.
type ResolveInput struct {
	Enabled  bool
	Priority []string
	// Candidates are keyed by priority entry. Missing entries are configuration errors.
	Candidates map[string]Candidate
}

// Resolve selects the first usable source in priority order. Native is always
// usable: whether a model can host search is decided per request, so a native
// binding also takes the first usable service after it as the fallback for
// models without it. Resolve returns an error only for configuration mistakes
// the user must fix; unusable but valid candidates are skipped with a
// diagnostic. It never performs failover after a request fails.
func Resolve(input ResolveInput) (Binding, error) {
	binding := Binding{Kind: BindingNone}
	if !input.Enabled {
		binding.Reason = "web search is disabled (web.search.enabled=false)"
		return binding, nil
	}
	if len(input.Priority) == 0 {
		binding.Reason = "web.search.priority is empty; no search source is allowed"
		return binding, nil
	}
	seen := make(map[string]struct{}, len(input.Priority))
	for index, entry := range input.Priority {
		if _, dup := seen[entry]; dup {
			return Binding{}, NewError(CodeInvalidConfig, "web.search.priority[%d]: duplicate entry %q", index, entry)
		}
		seen[entry] = struct{}{}
		if entry != PriorityNative && !strings.HasPrefix(entry, PriorityServicePrefix) {
			return Binding{}, NewError(CodeInvalidConfig, "web.search.priority[%d]: %q must be %q or %q<id>", index, entry, PriorityNative, PriorityServicePrefix)
		}
		candidate, ok := input.Candidates[entry]
		if !ok {
			if entry == PriorityNative {
				candidate = NativeCandidate()
			} else {
				return Binding{}, NewError(CodeInvalidConfig, "web.search.priority[%d]: service %q is not configured under web.services", index, strings.TrimPrefix(entry, PriorityServicePrefix))
			}
		}
		switch candidate.Availability {
		case AvailabilityReady:
			if binding.Kind == BindingNative {
				binding.Fallback = candidate
				return binding, nil
			}
			binding.Selected = candidate
			if entry != PriorityNative {
				binding.Kind = BindingService
				return binding, nil
			}
			binding.Kind = BindingNative
		case AvailabilityInvalidConfig:
			return Binding{}, NewError(CodeInvalidConfig, "web.services.%s: %s", candidate.InstanceID, candidate.Detail)
		case AvailabilityMissingCredentials:
			// A deliberately configured account without its key is a user
			// problem, not a reason to silently use another account.
			binding.Skipped = append(binding.Skipped, Diagnostic{Entry: entry, Availability: candidate.Availability, Detail: candidate.Detail})
			if binding.Kind == BindingNative {
				binding.Reason = fmt.Sprintf("no fallback service: %s: %s", entry, candidate.Detail)
			} else {
				binding.Reason = fmt.Sprintf("%s: %s", entry, candidate.Detail)
			}
			return binding, nil
		default:
			binding.Skipped = append(binding.Skipped, Diagnostic{Entry: entry, Availability: candidate.Availability, Detail: candidate.Detail})
		}
	}
	if binding.Kind == BindingNative {
		binding.Reason = "no fallback service after native in web.search.priority"
		return binding, nil
	}
	binding.Reason = "no search source in web.search.priority is usable"
	return binding, nil
}

// NativeCandidate describes the native entry. It is statically ready: the
// request's model decides whether hosted search applies.
func NativeCandidate() Candidate {
	return Candidate{
		Entry:        PriorityNative,
		Availability: AvailabilityReady,
		Detail:       "provider-hosted search for models that support it",
	}
}

// ValidateInstanceID checks the shared instance identifier rule.
func ValidateInstanceID(id string) error {
	if !InstanceIDPattern.MatchString(id) {
		return fmt.Errorf("instance id %q must match %s", id, InstanceIDPattern.String())
	}
	return nil
}

// ServiceEntry formats the priority entry for an instance.
func ServiceEntry(instanceID string) string {
	return PriorityServicePrefix + instanceID
}
