package web

import (
	"fmt"
	"regexp"
	"strings"
)

// PriorityNative is the priority entry reserved for model-native search.
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
	// BindingNative is reserved for provider-native search; no implementation
	// exists in this phase and the resolver never returns it.
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
	AvailabilityNotImplemented     Availability = "not_implemented"
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

// Resolve selects the first usable source in priority order. It returns an
// error only for configuration mistakes the user must fix; unusable but valid
// candidates are skipped with a diagnostic. It never performs failover after
// execution: that is a later, separately designed capability.
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
			binding.Kind = BindingService
			if entry == PriorityNative {
				binding.Kind = BindingNative
			}
			binding.Selected = candidate
			return binding, nil
		case AvailabilityInvalidConfig:
			return Binding{}, NewError(CodeInvalidConfig, "web.services.%s: %s", candidate.InstanceID, candidate.Detail)
		case AvailabilityMissingCredentials:
			// A deliberately configured account without its key is a user
			// problem, not a reason to silently use another account.
			binding.Skipped = append(binding.Skipped, Diagnostic{Entry: entry, Availability: candidate.Availability, Detail: candidate.Detail})
			binding.Reason = fmt.Sprintf("%s: %s", entry, candidate.Detail)
			return binding, nil
		default:
			binding.Skipped = append(binding.Skipped, Diagnostic{Entry: entry, Availability: candidate.Availability, Detail: candidate.Detail})
		}
	}
	binding.Reason = "no search source in web.search.priority is usable"
	return binding, nil
}

// NativeCandidate describes the reserved native entry. It is not implemented
// in this phase; the entry remains meaningful so users can already order it.
func NativeCandidate() Candidate {
	return Candidate{
		Entry:        PriorityNative,
		Availability: AvailabilityNotImplemented,
		Detail:       "model-native search is not implemented in this version",
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
