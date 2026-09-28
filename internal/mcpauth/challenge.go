package mcpauth

import (
	"context"
	"net/http"
	"strings"
)

// Probe performs one unauthenticated GET to obtain discovery/scope guidance.
// It never sends saved headers, follows a redirect, reads an SSE stream, or
// retries the original operation. Only an explicit login should call it.
func (c Client) Probe(ctx context.Context, resource string) (Discovery, error) {
	in := Discovery{Resource: resource}
	if _, err := canonicalResource(resource); err != nil {
		return in, err
	}
	resp, cancel, err := c.request(ctx, http.MethodGet, resource, "", nil, nil)
	if err != nil {
		return in, err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMethodNotAllowed {
			return in, nil
		}
		return in, &HTTPError{StatusCode: resp.StatusCode}
	}
	params, err := bearerParameters(resp.Header.Values("WWW-Authenticate"))
	if err != nil {
		return in, err
	}
	in.MetadataURL = params["resource_metadata"]
	if in.MetadataURL != "" {
		if _, ok := validURL(in.MetadataURL); !ok {
			return in, ErrMetadata
		}
	}
	if scope, present := params["scope"]; present {
		in.Scopes = []string{}
		if scope != "" {
			in.Scopes = strings.Split(scope, " ")
		}
		if !validScopes(in.Scopes) {
			return in, ErrMetadata
		}
	}
	return in, nil
}

// Commas inside quoted values are not challenge separators. Ambiguous multiple
// Bearer challenges or duplicate parameters fail closed rather than selecting
// whichever resource/scope the parser happened to see last.
func bearerParameters(headers []string) (map[string]string, error) {
	params := make(map[string]string)
	found, bearer := false, false
	size := 0
	for _, header := range headers {
		size += len(header)
		if size > 32<<10 {
			return nil, ErrLimit
		}
		parts, ok := splitChallenges(header)
		if !ok {
			return nil, ErrMetadata
		}
		bearer = false
		for _, part := range parts {
			part = strings.TrimSpace(part)
			key, rest := authToken(part)
			if key == "" {
				return nil, ErrMetadata
			}
			rest = strings.TrimSpace(rest)
			if !strings.HasPrefix(rest, "=") {
				bearer = strings.EqualFold(key, "Bearer")
				if bearer {
					if found {
						return nil, ErrMetadata
					}
					found = true
				}
				if rest == "" {
					continue
				}
				key, rest = authToken(rest)
				rest = strings.TrimSpace(rest)
			}
			if !bearer {
				continue
			}
			if key == "" || !strings.HasPrefix(rest, "=") {
				return nil, ErrMetadata
			}
			key = strings.ToLower(key)
			if _, duplicate := params[key]; duplicate {
				return nil, ErrMetadata
			}
			value, ok := authValue(strings.TrimSpace(rest[1:]))
			if !ok {
				return nil, ErrMetadata
			}
			params[key] = value
		}
	}
	return params, nil
}

func splitChallenges(header string) ([]string, bool) {
	var parts []string
	quoted, escaped, start := false, false, 0
	for i, r := range header {
		if r < 0x20 && r != '\t' || r == 0x7f {
			return nil, false
		}
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
		}
		if r == ',' && !quoted {
			parts = append(parts, header[start:i])
			start = i + 1
		}
	}
	if quoted || escaped {
		return nil, false
	}
	parts = append(parts, header[start:])
	return parts, true
}

func authToken(s string) (string, string) {
	for i, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func authValue(s string) (string, bool) {
	if !strings.HasPrefix(s, "\"") {
		value, rest := authToken(s)
		return value, value != "" && rest == ""
	}
	var value strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '"' {
			return value.String(), i == len(s)-1
		}
		if s[i] == '\\' {
			i++
			if i == len(s) {
				return "", false
			}
		}
		value.WriteByte(s[i])
	}
	return "", false
}
