package interaction

import "encoding/json"

// MCPRequest is a user-management action, never a model tool call. Definition
// contains a configuration value; Secret is transient input and must not enter
// conversation history or diagnostic output.
type MCPRequest struct {
	Action          string
	Key             string
	Fingerprint     string
	Definition      json.RawMessage
	Slot            string
	Secret          string
	PermissionScope string
	Permission      *MCPPermission
}

// MCPPermission describes one explicit operation version. Eligible reflects
// configuration restrictions, not execution authorization.
type MCPPermission struct {
	Operation         string `json:"operation,omitempty"`
	Tool              string `json:"tool"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	Decision          string `json:"decision"`
	Eligible          bool   `json:"eligible"`
}

type MCPService struct {
	Key                        string          `json:"key"`
	Name                       string          `json:"name,omitempty"`
	Source                     string          `json:"source"`
	State                      string          `json:"state"`
	Detail                     string          `json:"detail,omitempty"`
	Fingerprint                string          `json:"fingerprint"`
	Approval                   string          `json:"connection_approval"`
	Definition                 json.RawMessage `json:"connection"`
	ToolCount                  int             `json:"discovered_tools"`
	CatalogKnown               bool            `json:"catalog_known"`
	EligibleTools              int             `json:"eligible_tools"`
	PermissionScope            string          `json:"permission_scope"`
	Permissions                []MCPPermission `json:"saved_permissions,omitempty"`
	SavedPermissionFingerprint string          `json:"saved_permission_fingerprint,omitempty"`
	SavedPermissionScope       string          `json:"saved_permission_scope,omitempty"`
}

// MCPResult separates a durable commit from a connection test. A later test
// failure cannot turn a successful configuration save into "not saved".
type MCPResult struct {
	Committed   bool            `json:"committed"`
	Message     string          `json:"message,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Services    []MCPService    `json:"services,omitempty"`
	Permissions []MCPPermission `json:"permissions,omitempty"`
}
