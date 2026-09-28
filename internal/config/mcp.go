package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const (
	mcpSettingsKey      = "mcp"
	mcpCredentialsKey   = "mcp_services"
	maxMCPSettingsBytes = 1 << 20
	maxMCPServers       = 64
	// ManagedCUAServerID cannot be supplied by user or project settings.
	ManagedCUAServerID = "cua"
)

// MCPSettings is a collection, loaded separately from Viper's deep merge.
// User and trusted-project servers have separate identities, even if IDs match.
type MCPSettings struct {
	Servers      map[string]MCPServerSettings `json:"servers,omitempty"`
	Restrictions []MCPRestriction             `json:"restrictions,omitempty"`
}

type MCPServerSettings struct {
	Name           string                 `json:"name,omitempty"`
	Enabled        *bool                  `json:"enabled,omitempty"`
	Required       bool                   `json:"required,omitempty"`
	Transport      string                 `json:"transport"`
	Command        string                 `json:"command,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Cwd            string                 `json:"cwd,omitempty"`
	Env            map[string]MCPValueRef `json:"env,omitempty"`
	URL            string                 `json:"url,omitempty"`
	Headers        map[string]MCPValueRef `json:"headers,omitempty"`
	ConnectTimeout string                 `json:"connect_timeout,omitempty"`
	CallTimeout    string                 `json:"call_timeout,omitempty"`
	IncludeTools   *[]string              `json:"include_tools,omitempty"`
	ExcludeTools   []string               `json:"exclude_tools,omitempty"`
	PinnedTools    []string               `json:"pinned_tools,omitempty"`
}

// MCPValueRef selects exactly one literal, named environment input, or secret
// slot. HTTP headers require references; literals are only for stdio env values.
// AuthRef is a slot in this source/server/connection's MCP credential namespace,
// never an arbitrary path into another service or provider's credentials.
type MCPValueRef struct {
	Value   *string `json:"value,omitempty"`
	Env     string  `json:"env,omitempty"`
	AuthRef string  `json:"auth_ref,omitempty"`
	Prefix  string  `json:"prefix,omitempty"`
}

// MCPRestriction only denies. Source is user, project, or *; Server is an ID
// or *. An empty Tools list denies the whole service. Project restrictions may
// tighten user services but cannot lift user restrictions or rewrite bindings.
type MCPRestriction struct {
	Source string   `json:"source"`
	Server string   `json:"server"`
	Tools  []string `json:"tools,omitempty"`
}

type MCPConfig struct {
	Servers      map[string]MCPServer
	Restrictions []MCPRestriction
	connections  map[string]mcpConnectionApproval
}

// MCPServer is a frozen, configured identity, not a connection or a grant.
// CredentialScope binds stored slots to connection parameters; Fingerprint also
// changes when resolved credentials change, preventing stale grant reuse.
type MCPServer struct {
	Key, ID                      string
	Source                       Source
	Settings                     MCPServerSettings
	CredentialScope, Fingerprint string
	Enabled                      bool
	ConnectTimeout, CallTimeout  time.Duration
	MissingValues                []string
	environment, headers         map[string]string
}

func (s MCPServer) String() string { return s.Key }

// ConnectionValues transfers resolved values only to the connection owner.
// These maps contain secrets and must never be rendered or persisted as config.
func (s MCPServer) ConnectionValues() (environment, headers map[string]string) {
	return maps.Clone(s.environment), maps.Clone(s.headers)
}

func (m MCPConfig) Clone() MCPConfig {
	next := m
	next.Servers = make(map[string]MCPServer, len(m.Servers))
	for key, server := range m.Servers {
		server.Settings = cloneMCPServer(server.Settings)
		server.MissingValues = slices.Clone(server.MissingValues)
		server.environment, server.headers = server.ConnectionValues()
		next.Servers[key] = server
	}
	next.Restrictions = cloneMCPRestrictions(m.Restrictions)
	next.connections = maps.Clone(m.connections)
	return next
}

func (m MCPConfig) ServerKeys() []string {
	keys := slices.Collect(maps.Keys(m.Servers))
	slices.Sort(keys)
	return keys
}

// ServerAllowed applies enablement and whole-service restrictions. It is not
// connection approval: even an allowed project command needs explicit approval.
func (m MCPConfig) ServerAllowed(key string) bool {
	server, ok := m.Servers[key]
	if !ok || !server.Enabled {
		return false
	}
	for _, rule := range m.Restrictions {
		if (rule.Source == "*" || rule.Source == server.Source.Kind) && (rule.Server == "*" || rule.Server == server.ID) && len(rule.Tools) == 0 {
			return false
		}
	}
	return true
}

// ToolAllowed applies only configuration restrictions, never authorization.
func (m MCPConfig) ToolAllowed(key, name string) bool {
	server, ok := m.Servers[key]
	if !ok || !m.ServerAllowed(key) {
		return false
	}
	for _, rule := range m.Restrictions {
		if (rule.Source == "*" || rule.Source == server.Source.Kind) && (rule.Server == "*" || rule.Server == server.ID) &&
			(len(rule.Tools) == 0 || slices.Contains(rule.Tools, name)) {
			return false
		}
	}
	return (server.Settings.IncludeTools == nil || slices.Contains(*server.Settings.IncludeTools, name)) &&
		!slices.Contains(server.Settings.ExcludeTools, name)
}

func mcpID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func mcpText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && !strings.ContainsAny(text, "\x00\r\n")
}

func (s MCPSettings) Validate() error {
	if len(s.Servers) > maxMCPServers || len(s.Restrictions) > 256 {
		return fmt.Errorf("config: mcp collection limit exceeded")
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > maxMCPSettingsBytes {
		return fmt.Errorf("config: mcp settings exceed 1 MiB")
	}
	for id, server := range s.Servers {
		if !mcpID(id) || id == ManagedCUAServerID {
			return fmt.Errorf("config: mcp server ID must be lowercase and cannot use the reserved cua ID")
		}
		if err := server.validate(); err != nil {
			return fmt.Errorf("config: mcp.servers.%s: %w", id, err)
		}
	}
	for _, rule := range s.Restrictions {
		if rule.Source != "*" && rule.Source != "user" && rule.Source != "project" {
			return fmt.Errorf("config: mcp restriction source must be user, project, or *")
		}
		if rule.Server != "*" && !mcpID(rule.Server) {
			return fmt.Errorf("config: mcp restriction server must be an ID or *")
		}
		if !validMCPNames(rule.Tools) {
			return fmt.Errorf("config: mcp restriction tools must be distinct nonempty names")
		}
	}
	return nil
}

func (s MCPServerSettings) validate() error {
	if !mcpText(s.Name, 128) {
		return fmt.Errorf("name must be a single line of at most 128 bytes")
	}
	if _, err := mcpTimeout(s.ConnectTimeout, 15*time.Second); err != nil {
		return fmt.Errorf("connect_timeout: %w", err)
	}
	if _, err := mcpTimeout(s.CallTimeout, 60*time.Second); err != nil {
		return fmt.Errorf("call_timeout: %w", err)
	}
	if (s.IncludeTools != nil && !validMCPNames(*s.IncludeTools)) || !validMCPNames(s.ExcludeTools) || !validMCPNames(s.PinnedTools) {
		return fmt.Errorf("tool lists must contain distinct nonempty names")
	}
	for _, name := range s.PinnedTools {
		if slices.Contains(s.ExcludeTools, name) || s.IncludeTools != nil && !slices.Contains(*s.IncludeTools, name) {
			return fmt.Errorf("pinned tools must be included and not excluded")
		}
	}
	switch s.Transport {
	case "stdio":
		if !filepath.IsAbs(s.Command) || !filepath.IsAbs(s.Cwd) || !mcpText(s.Command, 4096) || !mcpText(s.Cwd, 4096) {
			return fmt.Errorf("stdio command and cwd must be explicit absolute paths")
		}
		if s.URL != "" || len(s.Headers) > 0 {
			return fmt.Errorf("stdio cannot include HTTP fields")
		}
		if len(s.Args) > 128 {
			return fmt.Errorf("stdio args exceed 128 entries")
		}
		for _, arg := range s.Args {
			if !mcpText(arg, 8192) {
				return fmt.Errorf("stdio argument exceeds its bound or contains a line break")
			}
		}
		if len(s.Env) > 64 {
			return fmt.Errorf("stdio env exceeds 64 entries")
		}
		for key, ref := range s.Env {
			if !mcpEnvName(key) || !ref.valid(true) {
				return fmt.Errorf("invalid stdio environment name or value reference")
			}
		}
	case "http":
		if s.Command != "" || s.Cwd != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("HTTP cannot include stdio fields")
		}
		if !validMCPEndpoint(s.URL) {
			return fmt.Errorf("url must be HTTPS or loopback HTTP, without userinfo or fragment")
		}
		if len(s.Headers) > 32 {
			return fmt.Errorf("HTTP headers exceed 32 entries")
		}
		seen := make(map[string]bool)
		for key, ref := range s.Headers {
			canonical := http.CanonicalHeaderKey(key)
			if !validMCPHeader(key) || seen[canonical] || !ref.valid(false) {
				return fmt.Errorf("invalid or duplicate HTTP header or credential reference")
			}
			seen[canonical] = true
		}
	default:
		return fmt.Errorf("transport must be stdio or http")
	}
	return nil
}

func validMCPNames(names []string) bool {
	if len(names) > 2000 {
		return false
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if strings.TrimSpace(name) == "" || !mcpText(name, 4096) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func mcpEnvName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, c := range name {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (r MCPValueRef) valid(literal bool) bool {
	count := 0
	if r.Value != nil {
		count++
		if !literal || !mcpText(*r.Value, 8192) {
			return false
		}
	}
	if r.Env != "" {
		count++
		if !mcpEnvName(r.Env) {
			return false
		}
	}
	if r.AuthRef != "" {
		count++
		if !mcpID(r.AuthRef) {
			return false
		}
	}
	return count == 1 && mcpText(r.Prefix, 128) && (r.Value == nil || r.Prefix == "")
}

func validMCPEndpoint(raw string) bool {
	if !mcpText(raw, 8192) || strings.ContainsAny(raw, " \t") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}

func validMCPHeader(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	switch http.CanonicalHeaderKey(key) {
	case "Host", "Content-Length", "Transfer-Encoding", "Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Idempotency-Key", "X-Idempotency-Key":
		return false
	}
	return true
}

func mcpTimeout(raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 || duration > 24*time.Hour {
		return 0, fmt.Errorf("must be a positive duration no longer than 24h")
	}
	return duration, nil
}

func cloneMCPServer(s MCPServerSettings) MCPServerSettings {
	if s.Enabled != nil {
		enabled := *s.Enabled
		s.Enabled = &enabled
	}
	s.Args = slices.Clone(s.Args)
	if s.IncludeTools != nil {
		names := slices.Clone(*s.IncludeTools)
		if names == nil {
			names = []string{}
		}
		s.IncludeTools = &names
	}
	s.ExcludeTools = slices.Clone(s.ExcludeTools)
	s.PinnedTools = slices.Clone(s.PinnedTools)
	s.Env = maps.Clone(s.Env)
	s.Headers = maps.Clone(s.Headers)
	for _, refs := range []map[string]MCPValueRef{s.Env, s.Headers} {
		for key, ref := range refs {
			if ref.Value != nil {
				value := *ref.Value
				ref.Value = &value
				refs[key] = ref
			}
		}
	}
	return s
}

func cloneMCPRestrictions(rules []MCPRestriction) []MCPRestriction {
	copy := slices.Clone(rules)
	for i := range copy {
		copy[i].Tools = slices.Clone(copy[i].Tools)
	}
	return copy
}

func decodeMCPSettings(raw json.RawMessage) (MCPSettings, error) {
	var settings MCPSettings
	if len(raw) == 0 {
		return settings, nil
	}
	if len(raw) > maxMCPSettingsBytes || jsonutil.DecodeStrict(raw, &settings) != nil || string(raw) == "null" {
		return settings, fmt.Errorf("config: invalid mcp settings object (values omitted)")
	}
	return settings, settings.Validate()
}

func mcpDigest(value any) string {
	data, _ := json.Marshal(value) // all callers use concrete JSON-safe value types
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
