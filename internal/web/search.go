package web

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/evidence"
)

// Search request bounds are AICE product values, not upstream guarantees.
const (
	MaxQueryBytes     = 4096
	DefaultMaxResults = 8
	MinResults        = 1
	MaxResults        = 20
)

// SearchRequest is the provider-neutral input to one search call. Domain lists
// hold bare hostnames only; the tool combines model and policy constraints
// before a backend sees them.
type SearchRequest struct {
	Query           string
	MaxResults      int
	AllowedDomains  []string
	ExcludedDomains []string
}

// Validate checks the model-controlled fields.
func (r SearchRequest) Validate() error {
	query := strings.TrimSpace(r.Query)
	if query == "" {
		return NewError(CodeInvalidArgument, "query is required")
	}
	if !utf8.ValidString(query) {
		return NewError(CodeInvalidArgument, "query must be valid UTF-8")
	}
	if len(query) > MaxQueryBytes {
		return NewError(CodeInvalidArgument, "query exceeds %d bytes", MaxQueryBytes)
	}
	if r.MaxResults != 0 && (r.MaxResults < MinResults || r.MaxResults > MaxResults) {
		return NewError(CodeInvalidArgument, "max_results must be between %d and %d", MinResults, MaxResults)
	}
	for _, domain := range r.AllowedDomains {
		if _, err := NormalizeDomain(domain); err != nil {
			return NewError(CodeInvalidArgument, "allowed_domains: %v", err)
		}
	}
	for _, domain := range r.ExcludedDomains {
		if _, err := NormalizeDomain(domain); err != nil {
			return NewError(CodeInvalidArgument, "excluded_domains: %v", err)
		}
	}
	return nil
}

// SearchCapabilities declares what a backend can enforce server-side. A tool
// must not silently drop a hard constraint the backend cannot express.
type SearchCapabilities struct {
	AllowedDomains  bool
	ExcludedDomains bool
	MaxResults      int
}

// SearchBackend is the consumer contract implemented by concrete adapters.
type SearchBackend interface {
	Capabilities() SearchCapabilities
	Search(context.Context, SearchRequest) (SearchResponse, error)
}

// SearchResponse is the normalized result of one search. Provider fields stop
// at the adapter; Evidence carries the sources and their excerpts.
type SearchResponse struct {
	InstanceID string
	ProviderID string
	APIID      string
	Query      string
	Evidence   evidence.Bundle
}
