package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/lockedfile"
)

// Credential slots are scoped by source-qualified server key and the hash of
// its connection configuration. A changed endpoint cannot receive the old key.
type mcpCredentials map[string]map[string]map[string]string

type mcpLayer struct {
	user, project json.RawMessage
	// projectFile is the trusted project's .mcp.json converted to MCPSettings.
	projectFile json.RawMessage
	credentials mcpCredentials
	oauth       mcpOAuthCredentials
	connections map[string]mcpConnectionApproval
	permissions mcpPermissions
	diagnostics []string
}

func (l *mcpLayer) extract(path string, values map[string]any, paths Paths) error {
	if raw, ok := values[mcpPermissionsKey]; ok {
		delete(values, mcpPermissionsKey)
		if path != paths.GlobalAuth {
			l.diagnostics = append(l.diagnostics, "Ignored mcp_permissions outside the user auth file")
		} else {
			permissions, err := decodeMCPPermissions(raw)
			if err != nil {
				return err
			}
			l.permissions = permissions
		}
	}
	if raw, ok := values[mcpOAuthKey]; ok {
		delete(values, mcpOAuthKey)
		if path != paths.GlobalAuth {
			l.diagnostics = append(l.diagnostics, "Ignored mcp_oauth outside the user auth file")
		} else {
			credentials, err := decodeMCPOAuth(raw)
			if err != nil {
				return err
			}
			l.oauth = credentials
		}
	}
	if raw, ok := values[mcpConnectionsKey]; ok {
		delete(values, mcpConnectionsKey)
		if path != paths.GlobalAuth {
			l.diagnostics = append(l.diagnostics, "Ignored mcp_connections outside the user auth file")
		} else {
			approvals, err := decodeMCPConnections(raw)
			if err != nil {
				return err
			}
			l.connections = approvals
		}
	}
	if raw, ok := values[mcpSettingsKey]; ok {
		delete(values, mcpSettingsKey)
		if path == paths.GlobalAuth {
			l.diagnostics = append(l.diagnostics, "Ignored mcp in auth file: connection configuration belongs in settings")
		} else {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return fmt.Errorf("config: cannot encode mcp settings")
			}
			if path == paths.ProjectSettings {
				l.project = encoded
			} else {
				l.user = encoded
			}
		}
	}
	if raw, ok := values[mcpCredentialsKey]; ok {
		delete(values, mcpCredentialsKey)
		if path != paths.GlobalAuth {
			l.diagnostics = append(l.diagnostics, "Ignored mcp_services outside the user auth file")
			return nil
		}
		credentials, err := decodeMCPCredentials(raw)
		if err != nil {
			return err
		}
		l.credentials = credentials
	}
	return nil
}

// readProjectFile loads the portable project file. Entries AICE cannot represent
// are skipped with a diagnostic instead of hiding the project's other services.
func (l *mcpLayer) readProjectFile(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := lockedfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	servers, skipped, err := ParseMCPServers(data)
	if err != nil {
		l.diagnostics = append(l.diagnostics, fmt.Sprintf("Ignored invalid MCP file %s: %v", path, err))
		return nil
	}
	for _, reason := range skipped {
		l.diagnostics = append(l.diagnostics, fmt.Sprintf("Skipped MCP server in %s: %s", path, reason))
	}
	if len(servers) == 0 {
		return nil
	}
	l.projectFile, err = json.Marshal(MCPSettings{Servers: servers})
	return err
}

func decodeMCPCredentials(raw any) (mcpCredentials, error) {
	data, err := json.Marshal(raw)
	var credentials mcpCredentials
	if err != nil || len(data) > maxMCPSettingsBytes || json.Unmarshal(data, &credentials) != nil || credentials == nil {
		return nil, fmt.Errorf("config: invalid mcp_services credential object (values omitted)")
	}
	for _, scopes := range credentials {
		if scopes == nil {
			return nil, fmt.Errorf("config: invalid MCP credential scope")
		}
		for scope, slots := range scopes {
			if len(scope) != 64 || slots == nil {
				return nil, fmt.Errorf("config: invalid MCP credential scope")
			}
			for name, value := range slots {
				if !mcpID(name) || !mcpText(value, 8192) {
					return nil, fmt.Errorf("config: invalid MCP credential slot (values omitted)")
				}
			}
		}
	}
	return credentials, nil
}

func effectiveMCP(layer mcpLayer, paths Paths, lookup func(string) (string, bool)) (MCPConfig, error) {
	user, err := decodeMCPSettings(layer.user)
	if err != nil {
		return MCPConfig{}, err
	}
	project, err := decodeMCPSettings(layer.project)
	if err != nil {
		return MCPConfig{}, err
	}
	projectFile, err := decodeMCPSettings(layer.projectFile)
	if err != nil {
		return MCPConfig{}, err
	}
	result := MCPConfig{Servers: make(map[string]MCPServer), connections: maps.Clone(layer.connections), permissions: cloneMCPPermissions(layer.permissions)}
	result.Restrictions = append(cloneMCPRestrictions(user.Restrictions), cloneMCPRestrictions(project.Restrictions)...)
	for _, input := range []struct {
		source   Source
		settings MCPSettings
	}{
		{Source{Kind: "user", Location: paths.GlobalSettings}, user},
		{Source{Kind: "project", Location: paths.ProjectSettings}, project},
		{Source{Kind: "project", Location: paths.ProjectMCP()}, projectFile},
	} {
		for id, settings := range input.settings.Servers {
			server, err := resolveMCPServer(id, input.source, settings, layer.credentials, lookup, paths.Workspace())
			if err != nil {
				return MCPConfig{}, err
			}
			server.applyOAuth(layer.oauth[server.Key][server.CredentialScope])
			result.Servers[server.Key] = server
		}
	}
	return result, nil
}

func resolveMCPServer(id string, source Source, settings MCPServerSettings, credentials mcpCredentials, lookup func(string) (string, bool), workspace string) (MCPServer, error) {
	if source.Location == "" {
		return MCPServer{}, fmt.Errorf("config: MCP configuration source path is required")
	}
	location, err := filepath.Abs(source.Location)
	if err != nil {
		return MCPServer{}, fmt.Errorf("config: resolve MCP configuration source")
	}
	source.Location = filepath.Clean(location)
	key := source.Kind + ":" + id
	if source.Kind == "project" {
		key = source.Kind + ":" + mcpDigest(source.Location) + ":" + id
	}
	settings = cloneMCPServer(settings)
	server := MCPServer{Key: key, ID: id, Source: source, Settings: settings, Enabled: true}
	if settings.Enabled != nil {
		server.Enabled = *settings.Enabled
	}
	server.ConnectTimeout, _ = mcpTimeout(settings.ConnectTimeout, 15*time.Second)
	server.CallTimeout, _ = mcpTimeout(settings.CallTimeout, 60*time.Second)
	// Labels, enablement, filtering and operation timeouts are policy/settings,
	// not connection parameters. Changing any connection field or credential
	// reference invalidates both its stored credential scope and connection grant.
	connection := settings
	connection.Name = ""
	connection.Enabled = nil
	connection.Required = false
	connection.IncludeTools = nil
	connection.ExcludeTools = nil
	connection.PinnedTools = nil
	connection.ConnectTimeout = ""
	connection.CallTimeout = ""
	server.CredentialScope = mcpDigest(struct {
		Source     Source
		ID         string
		Connection MCPServerSettings
	}{source, id, connection})
	slots := credentials[key][server.CredentialScope]
	server.environment, server.MissingValues = resolveMCPValues(settings.Env, slots, lookup, "env")
	headers, missing := resolveMCPValues(settings.Headers, slots, lookup, "headers")
	server.headers = headers
	server.MissingValues = append(server.MissingValues, missing...)
	if settings.OAuth != nil && settings.OAuth.ClientSecret != nil {
		values, missing := resolveMCPValues(map[string]MCPValueRef{"client_secret": *settings.OAuth.ClientSecret}, slots, lookup, "oauth")
		server.oauthClientSecret = values["client_secret"]
		server.MissingValues = append(server.MissingValues, missing...)
	}
	slices.Sort(server.MissingValues)
	if settings.Transport == "stdio" {
		server.Executable, server.WorkingDir = settings.Command, settings.Cwd
		if !filepath.IsAbs(settings.Command) {
			pathValue, ok := server.environment["PATH"]
			if !ok && lookup != nil {
				pathValue, _ = lookup("PATH")
			}
			server.Executable = mcpLookPath(settings.Command, pathValue, lookup)
		}
		if server.WorkingDir == "" {
			server.WorkingDir = workspace
		}
	}
	server.Fingerprint = server.connectionFingerprint()
	return server, nil
}

// mcpLookPath resolves a bare command against an explicit PATH value without
// executing anything. Relative PATH entries are ignored. An unresolved command
// returns "" and the connection owner reports it before launching.
func mcpLookPath(name, pathValue string, lookup func(string) (string, bool)) string {
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		value := ".COM;.EXE;.BAT;.CMD"
		if lookup != nil {
			if configured, ok := lookup("PATHEXT"); ok && configured != "" {
				value = configured
			}
		}
		extensions = append(extensions, strings.Split(value, ";")...)
	}
	for _, dir := range filepath.SplitList(pathValue) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, extension := range extensions {
			candidate := filepath.Join(dir, name+extension)
			info, err := os.Stat(candidate)
			if err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0) {
				return candidate
			}
		}
	}
	return ""
}

func resolveMCPValues(refs map[string]MCPValueRef, slots map[string]string, lookup func(string) (string, bool), kind string) (map[string]string, []string) {
	values := make(map[string]string, len(refs))
	var missing []string
	for name, ref := range refs {
		var value string
		present := false
		switch {
		case ref.Value != nil:
			value, present = *ref.Value, true
		case ref.Env != "":
			if lookup != nil {
				value, present = lookup(ref.Env)
				present = present && value != ""
			}
		case ref.AuthRef != "":
			value, present = slots[ref.AuthRef]
			present = present && value != ""
		}
		if !present || !mcpText(value, 8192) {
			missing = append(missing, kind+"."+name)
			continue
		}
		values[name] = ref.Prefix + value
	}
	return values, missing
}

// WithMCP replaces the user collection after a successful save, preserving
// frozen project restrictions and inputs. It never launches a connection.
func (c Config) WithMCP(settings MCPSettings) (Config, error) {
	if err := settings.Validate(); err != nil {
		return Config{}, err
	}
	next := c
	next.mcpInputs.user, _ = json.Marshal(settings)
	effective, err := effectiveMCP(next.mcpInputs, c.Paths, c.environmentLookup)
	if err != nil {
		return Config{}, err
	}
	next.MCP = effective
	return next, nil
}

// WithMCPCredential records a just-saved slot in this instance without loading
// files again. The supplied server must belong to this exact frozen snapshot.
func (c Config) WithMCPCredential(key, scope, slot, value string) (Config, error) {
	server, ok := c.MCP.Servers[key]
	if !ok || server.CredentialScope != scope || !mcpID(slot) || !mcpText(value, 8192) {
		return Config{}, fmt.Errorf("config: invalid MCP credential binding")
	}
	next := c
	next.mcpInputs.credentials = cloneMCPCredentials(c.mcpInputs.credentials)
	setMCPCredential(next.mcpInputs.credentials, key, scope, slot, value)
	effective, err := effectiveMCP(next.mcpInputs, c.Paths, c.environmentLookup)
	if err != nil {
		return Config{}, err
	}
	next.MCP = effective
	return next, nil
}

func cloneMCPCredentials(credentials mcpCredentials) mcpCredentials {
	copy := make(mcpCredentials, len(credentials))
	for key, scopes := range credentials {
		copy[key] = make(map[string]map[string]string, len(scopes))
		for scope, slots := range scopes {
			copy[key][scope] = maps.Clone(slots)
		}
	}
	return copy
}

func setMCPCredential(credentials mcpCredentials, key, scope, slot, value string) {
	if value == "" {
		delete(credentials[key][scope], slot)
		if len(credentials[key][scope]) == 0 {
			delete(credentials[key], scope)
		}
		if len(credentials[key]) == 0 {
			delete(credentials, key)
		}
		return
	}
	if credentials[key] == nil {
		credentials[key] = make(map[string]map[string]string)
	}
	if credentials[key][scope] == nil {
		credentials[key][scope] = make(map[string]string)
	}
	credentials[key][scope][slot] = value
}
