package web

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

// NormalizeDomain accepts a bare hostname: lowercase, IDNA ASCII form, no
// scheme, path, port, wildcard or trailing dot. URL prefixes and wildcard
// expressions are not part of the first-phase domain semantics.
func NormalizeDomain(raw string) (string, error) {
	domain := strings.TrimSpace(raw)
	if domain == "" {
		return "", fmt.Errorf("domain is empty")
	}
	if strings.ContainsAny(domain, " \t\r\n/:@?#*") {
		return "", fmt.Errorf("domain %q must be a bare hostname", raw)
	}
	domain = strings.TrimSuffix(domain, ".")
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(domain))
	if err != nil {
		return "", fmt.Errorf("domain %q is not a valid hostname: %v", raw, err)
	}
	if ascii == "" || strings.HasPrefix(ascii, ".") || strings.Contains(ascii, "..") {
		return "", fmt.Errorf("domain %q is not a valid hostname", raw)
	}
	return ascii, nil
}

// domainMatches reports whether host equals domain or is one of its
// subdomains, using a label boundary so notexample.com never matches example.com.
func domainMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// DomainPolicy is the user-level upper bound on where a search may look.
type DomainPolicy struct {
	Allowed  []string
	Excluded []string
}

// CombineDomains applies the model's request within the policy: allow lists
// intersect semantically (a model domain must be within a policy domain), deny
// lists union. An empty resulting allow list when both sides supplied one is a
// conflict; it is never sent upstream as "no restriction".
func CombineDomains(policy DomainPolicy, requested SearchRequest) ([]string, []string, error) {
	policyAllowed, err := normalizeDomains(policy.Allowed)
	if err != nil {
		return nil, nil, NewError(CodeInvalidConfig, "allowed_domains policy: %v", err)
	}
	policyExcluded, err := normalizeDomains(policy.Excluded)
	if err != nil {
		return nil, nil, NewError(CodeInvalidConfig, "excluded_domains policy: %v", err)
	}
	requestAllowed, err := normalizeDomains(requested.AllowedDomains)
	if err != nil {
		return nil, nil, NewError(CodeInvalidArgument, "allowed_domains: %v", err)
	}
	requestExcluded, err := normalizeDomains(requested.ExcludedDomains)
	if err != nil {
		return nil, nil, NewError(CodeInvalidArgument, "excluded_domains: %v", err)
	}

	var allowed []string
	switch {
	case len(policyAllowed) == 0:
		allowed = requestAllowed
	case len(requestAllowed) == 0:
		allowed = policyAllowed
	default:
		for _, candidate := range requestAllowed {
			for _, bound := range policyAllowed {
				if domainMatches(candidate, bound) {
					allowed = append(allowed, candidate)
					break
				}
			}
		}
		if len(allowed) == 0 {
			return nil, nil, NewError(CodeConstraintConflict,
				"requested allowed_domains %v fall outside the configured policy %v", requestAllowed, policyAllowed)
		}
	}
	excluded := append(append([]string(nil), policyExcluded...), requestExcluded...)
	slices.Sort(excluded)
	excluded = slices.Compact(excluded)
	if len(excluded) == 0 {
		excluded = nil
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	return allowed, excluded, nil
}

func normalizeDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(domains))
	for _, domain := range domains {
		value, err := NormalizeDomain(domain)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	slices.Sort(normalized)
	return slices.Compact(normalized), nil
}
