package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

// portableMCPServer is the de facto server entry shared by .mcp.json, Claude
// Desktop, Cursor and VS Code. Unknown host-specific fields are ignored.
type portableMCPServer struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Cwd      string            `json:"cwd"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`
}

// A value is either literal text or ends with one ${NAME} reference, such as
// "Bearer ${TOKEN}". Anything else that contains "${" is not representable.
var portableMCPReference = regexp.MustCompile(`^(.*?)\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// ParseMCPServerDefinition accepts one AICE definition, recognized by its
// "transport" field and decoded strictly, or one portable entry.
func ParseMCPServerDefinition(raw []byte) (MCPServerSettings, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return MCPServerSettings{}, fmt.Errorf("invalid MCP server definition (values omitted)")
	}
	if _, native := fields["transport"]; native {
		var settings MCPServerSettings
		if jsonutil.DecodeStrict(raw, &settings) != nil {
			return MCPServerSettings{}, fmt.Errorf("invalid MCP server definition (values omitted)")
		}
		return settings, settings.validate()
	}
	var portable portableMCPServer
	if err := json.Unmarshal(raw, &portable); err != nil {
		return MCPServerSettings{}, fmt.Errorf("invalid MCP server definition (values omitted)")
	}
	settings, err := portable.settings()
	if err != nil {
		return MCPServerSettings{}, err
	}
	return settings, settings.validate()
}

// ParseMCPServers converts a portable {"mcpServers": {...}} or VS Code
// {"servers": {...}} document. Entries that cannot be represented are skipped
// with a reason; the error reports only an unusable document.
func ParseMCPServers(raw []byte) (map[string]MCPServerSettings, []string, error) {
	var document struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Servers    map[string]json.RawMessage `json:"servers"`
	}
	if len(raw) > maxMCPSettingsBytes || json.Unmarshal(raw, &document) != nil {
		return nil, nil, fmt.Errorf("not a JSON object of at most 1 MiB")
	}
	entries := document.MCPServers
	if entries == nil {
		entries = document.Servers
	}
	if entries == nil {
		return nil, nil, fmt.Errorf(`expected an "mcpServers" or "servers" object`)
	}
	servers := make(map[string]MCPServerSettings)
	var skipped []string
	for name, entry := range entries {
		id := portableMCPID(name)
		if id == "" {
			skipped = append(skipped, fmt.Sprintf("%q: name cannot be used as a server ID", name))
			continue
		}
		if _, exists := servers[id]; exists {
			skipped = append(skipped, fmt.Sprintf("%q: duplicates server ID %s", name, id))
			continue
		}
		settings, err := ParseMCPServerDefinition(entry)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%q: %v", name, err))
			continue
		}
		if settings.Name == "" && name != id && mcpText(name, 128) {
			settings.Name = name
		}
		servers[id] = settings
	}
	if len(servers) > maxMCPServers {
		return nil, nil, fmt.Errorf("more than %d servers", maxMCPServers)
	}
	return servers, skipped, nil
}

func (p portableMCPServer) settings() (MCPServerSettings, error) {
	settings := MCPServerSettings{}
	if p.Disabled {
		disabled := false
		settings.Enabled = &disabled
	}
	switch strings.ToLower(p.Type) {
	case "":
		if p.URL != "" {
			settings.Transport = "http"
		} else {
			settings.Transport = "stdio"
		}
	case "stdio":
		settings.Transport = "stdio"
	case "http", "streamable-http", "streamablehttp":
		settings.Transport = "http"
	case "sse":
		return settings, fmt.Errorf("the deprecated HTTP+SSE transport is unsupported; use Streamable HTTP")
	default:
		return settings, fmt.Errorf("unsupported transport %q", p.Type)
	}
	if settings.Transport == "stdio" {
		if strings.Contains(p.Command, "${") || strings.Contains(p.Cwd, "${") {
			return settings, fmt.Errorf("command and cwd cannot use ${} expansion")
		}
		for _, arg := range p.Args {
			if strings.Contains(arg, "${") {
				return settings, fmt.Errorf("arguments cannot use ${} expansion; pass values through env")
			}
		}
		settings.Command, settings.Args, settings.Cwd = p.Command, p.Args, p.Cwd
		for key, value := range p.Env {
			ref, err := portableMCPValue(value, true)
			if err != nil {
				return settings, fmt.Errorf("env %s: %w", key, err)
			}
			if settings.Env == nil {
				settings.Env = make(map[string]MCPValueRef)
			}
			settings.Env[key] = ref
		}
		return settings, nil
	}
	if strings.Contains(p.URL, "${") {
		return settings, fmt.Errorf("url cannot use ${} expansion")
	}
	settings.URL = p.URL
	for key, value := range p.Headers {
		ref, err := portableMCPValue(value, false)
		if err != nil {
			return settings, fmt.Errorf("header %s: %w", key, err)
		}
		if settings.Headers == nil {
			settings.Headers = make(map[string]MCPValueRef)
		}
		settings.Headers[key] = ref
	}
	return settings, nil
}

// portableMCPValue maps ${NAME} to an environment reference. HTTP headers may
// carry credentials, so they never keep literal values in settings.
func portableMCPValue(value string, literal bool) (MCPValueRef, error) {
	if match := portableMCPReference.FindStringSubmatch(value); match != nil && !strings.Contains(match[1], "${") {
		return MCPValueRef{Env: match[2], Prefix: match[1]}, nil
	}
	if strings.Contains(value, "${") {
		return MCPValueRef{}, fmt.Errorf("only a trailing ${NAME} reference is supported")
	}
	if !literal {
		return MCPValueRef{}, fmt.Errorf("literal values are not stored; use ${ENV_NAME} or an auth_ref credential slot")
	}
	return MCPValueRef{Value: &value}, nil
}

// portableMCPID lowercases a host-specific server name into an AICE ID.
func portableMCPID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.' || r == ' ' || r == '/' || r == ':' || r == '@':
			b.WriteByte('-')
		default:
			return ""
		}
	}
	id := strings.Trim(b.String(), "-_")
	if !mcpID(id) {
		return ""
	}
	return id
}
