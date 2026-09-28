package mcpauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Server is a validated discovery snapshot. Unexported fields prevent callers
// from substituting an endpoint between discovery and a credential exchange.
type Server struct {
	resource, issuer, authorization, token, registration string
	scopes, authMethods                                  []string
	responseIssuer                                       bool
}

func (s Server) Resource() string      { return s.resource }
func (s Server) Issuer() string        { return s.issuer }
func (s Server) TokenEndpoint() string { return s.token }
func (s Server) Scopes() []string      { return slices.Clone(s.scopes) }

// Discovery specifies an already user-authorized resource. MetadataURL and
// Scopes may come from a Bearer challenge; Issuer chooses among multiple ASs.
// A nil Scopes uses the protected resource's advertised scopes.
type Discovery struct {
	Resource    string
	MetadataURL string
	Issuer      string
	Scopes      []string
}

func (c Client) Discover(ctx context.Context, in Discovery) (Server, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resource, err := canonicalResource(in.Resource)
	if err != nil || !validScopes(in.Scopes) {
		return Server{}, ErrConfig
	}
	u, _ := url.Parse(resource)
	locations := []string{wellKnown(u, "oauth-protected-resource", false)}
	root := *u
	root.Path, root.RawPath = "", ""
	if fallback := wellKnown(&root, "oauth-protected-resource", false); fallback != locations[0] {
		locations = append(locations, fallback)
	}
	if in.MetadataURL != "" {
		if !endpointFor(resource, in.MetadataURL) {
			return Server{}, ErrMetadata
		}
		locations = []string{in.MetadataURL}
	}
	var protected struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
		BearerMethods        []string `json:"bearer_methods_supported"`
	}
	if err := c.discoverJSON(ctx, locations, &protected); err != nil {
		return Server{}, err
	}
	got, err := canonicalResource(protected.Resource)
	if err != nil || got != resource || len(protected.AuthorizationServers) == 0 || len(protected.AuthorizationServers) > 16 || !validScopes(protected.Scopes) {
		return Server{}, ErrMetadata
	}
	if len(protected.BearerMethods) != 0 && !slices.Contains(protected.BearerMethods, "header") {
		return Server{}, ErrMetadata
	}
	issuer := in.Issuer
	if issuer == "" {
		if len(protected.AuthorizationServers) != 1 {
			return Server{}, ErrMetadata
		}
		issuer = protected.AuthorizationServers[0]
	}
	if !slices.Contains(protected.AuthorizationServers, issuer) {
		return Server{}, ErrMetadata
	}
	u, ok := validURL(issuer)
	if !ok || u.RawQuery != "" || u.ForceQuery || !endpointFor(resource, issuer) {
		return Server{}, ErrMetadata
	}
	locations = []string{wellKnown(u, "oauth-authorization-server", false), wellKnown(u, "openid-configuration", false)}
	if u.Path != "" && u.Path != "/" {
		locations = append(locations, wellKnown(u, "openid-configuration", true))
	}
	var metadata struct {
		Issuer           string   `json:"issuer"`
		Authorization    string   `json:"authorization_endpoint"`
		Token            string   `json:"token_endpoint"`
		Registration     string   `json:"registration_endpoint"`
		ChallengeMethods []string `json:"code_challenge_methods_supported"`
		AuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
		ResponseTypes    []string `json:"response_types_supported"`
		GrantTypes       []string `json:"grant_types_supported"`
		ResponseIssuer   bool     `json:"authorization_response_iss_parameter_supported"`
	}
	if err := c.discoverJSON(ctx, locations, &metadata); err != nil {
		return Server{}, err
	}
	if metadata.Issuer != issuer || !slices.Contains(metadata.ChallengeMethods, "S256") || !slices.Contains(metadata.ResponseTypes, "code") {
		return Server{}, ErrMetadata
	}
	if len(metadata.GrantTypes) != 0 && !slices.Contains(metadata.GrantTypes, "authorization_code") {
		return Server{}, ErrMetadata
	}
	for _, endpoint := range []string{metadata.Authorization, metadata.Token} {
		if !endpointFor(issuer, endpoint) {
			return Server{}, ErrMetadata
		}
	}
	if metadata.Registration != "" {
		if !endpointFor(issuer, metadata.Registration) {
			return Server{}, ErrMetadata
		}
	}
	if len(metadata.AuthMethods) == 0 {
		metadata.AuthMethods = []string{"client_secret_basic"}
	}
	scopes := protected.Scopes
	if in.Scopes != nil {
		scopes = in.Scopes
	}
	return Server{resource: resource, issuer: issuer, authorization: metadata.Authorization, token: metadata.Token,
		registration: metadata.Registration, scopes: slices.Clone(scopes), authMethods: slices.Clone(metadata.AuthMethods), responseIssuer: metadata.ResponseIssuer}, nil
}

func wellKnown(u *url.URL, name string, appendPath bool) string {
	next := *u
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	if appendPath {
		next.RawPath = path + "/.well-known/" + name
	} else {
		next.RawPath = "/.well-known/" + name + path
	}
	next.Path, _ = url.PathUnescape(next.RawPath)
	next.RawQuery, next.ForceQuery, next.Fragment = "", false, ""
	return next.String()
}

func (c Client) discoverJSON(ctx context.Context, locations []string, out any) error {
	for i, endpoint := range locations {
		err := c.exchangeJSON(ctx, http.MethodGet, endpoint, "", nil, nil, out)
		var status *HTTPError
		if err == nil || i == len(locations)-1 || !errors.As(err, &status) || (status.StatusCode != 404 && status.StatusCode != 405) {
			return err
		}
	}
	return ErrMetadata
}
