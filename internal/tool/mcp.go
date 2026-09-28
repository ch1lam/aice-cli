package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// MCPBackend executes a single call on an application-bound connection. The
// application rechecks its catalog/connection before dispatch. No retry belongs
// in this adapter, including after returned errors and unknown outcomes.
type MCPBackend interface {
	Call(context.Context, string, json.RawMessage) (mcpclient.Result, error)
}

// MCPOptions supplies one frozen version, never a model-chosen remote name.
type MCPOptions struct {
	Definition llm.ToolDefinition
	Binding    llm.ToolBinding
	Backend    MCPBackend
	// Secrets are explicitly supplied by the connection owner for literal
	// redaction. They are not discovered from the process environment.
	Secrets []string
}

type MCP struct {
	definition llm.ToolDefinition
	binding    llm.ToolBinding
	backend    MCPBackend
	redact     mcpRedactor
}

func NewMCP(options MCPOptions) (*MCP, error) {
	if options.Backend == nil {
		return nil, fmt.Errorf("tool: MCP backend is required")
	}
	if err := options.Definition.Validate(); err != nil {
		return nil, fmt.Errorf("tool: invalid MCP definition")
	}
	if err := (&llm.ToolResultDetails{State: llm.ExecutionNotDispatched, Binding: &options.Binding}).Validate(); err != nil {
		return nil, fmt.Errorf("tool: invalid MCP binding")
	}
	redact := newMCPRedactor(options.Secrets)
	_, schemaRedacted, schemaErr := redact.json(options.Definition.InputSchema)
	// Schemas must remain whole and exact. A credential-bearing definition
	// cannot be repaired by editing schema strings behind the server's back.
	if schemaErr != nil || schemaRedacted || redact.contains(options.Definition.Name) ||
		redact.contains(options.Binding.Source) || redact.contains(options.Binding.ServiceID) || redact.contains(options.Binding.ToolName) {
		return nil, fmt.Errorf("tool: MCP definition contains a configured credential")
	}
	definition := options.Definition
	definition.InputSchema = slices.Clone(definition.InputSchema)
	definition.Description = redact.text(definition.Description)
	// Remote descriptions are data, not application-authored prompt guidance.
	definition.PromptSnippet, definition.PromptGuidelines = "", nil
	return &MCP{definition: definition, binding: options.Binding, backend: options.Backend, redact: redact}, nil
}

func (m *MCP) Definition() llm.ToolDefinition {
	definition := m.definition
	definition.InputSchema = slices.Clone(definition.InputSchema)
	return definition
}

func (m *MCP) ToolBinding() llm.ToolBinding { return m.binding }

func (m *MCP) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	fail := func(message string) (llm.ToolResult, error) {
		result := textResult(call, message, true)
		binding := m.binding
		result.Details = &llm.ToolResultDetails{State: llm.ExecutionNotDispatched, Binding: &binding}
		return result, nil
	}
	if ctx == nil || ctx.Err() != nil {
		return fail("MCP call canceled before dispatch.")
	}
	if call.Name != m.definition.Name {
		return fail("MCP call does not match its bound tool.")
	}
	arguments := bytes.TrimSpace(call.Arguments)
	if len(arguments) == 0 || len(arguments) > 1<<20 || arguments[0] != '{' ||
		!json.Valid(arguments) || !utf8.Valid(arguments) {
		return fail("MCP arguments must be a complete UTF-8 JSON object of at most 1 MiB.")
	}
	result, err := m.backend.Call(ctx, m.binding.ToolName, slices.Clone(arguments))
	// Preserve a returned partial result even after cancellation. Media
	// conversion is bounded by its own storage limits and a cleanup deadline.
	return m.mapResult(call, result, err), nil
}

type mcpRedactor struct{ secrets []string }

func newMCPRedactor(secrets []string) mcpRedactor {
	r := mcpRedactor{}
	for _, secret := range secrets {
		if secret != "" && !slices.Contains(r.secrets, secret) {
			r.secrets = append(r.secrets, secret)
		}
	}
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
	return r
}

func (r mcpRedactor) contains(text string) bool {
	for _, secret := range r.secrets {
		if strings.Contains(text, secret) {
			return true
		}
	}
	return false
}

func (r mcpRedactor) text(text string) string {
	for _, secret := range r.secrets {
		text = strings.ReplaceAll(text, secret, "[credential redacted]")
	}
	if r.contains(text) {
		return "" // even the replacement marker can match a short credential
	}
	return text
}

// json preserves source bytes and number lexemes when no credential occurs.
// It decodes JSON strings too, so Unicode/escape spelling cannot hide a literal
// known value. Key collisions or excessive nesting fail closed without saving.
func (r mcpRedactor) json(raw json.RawMessage) (json.RawMessage, bool, error) {
	if !json.Valid(raw) || !utf8.Valid(raw) {
		return nil, false, fmt.Errorf("invalid JSON")
	}
	if len(r.secrets) == 0 {
		return slices.Clone(raw), false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, fmt.Errorf("invalid JSON")
	}
	changed := r.contains(string(raw))
	// Inspect every original JSON string, including overwritten duplicate keys.
	// Decoding only the final map would miss secrets in an earlier duplicate.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		start := i
		i++
		for ; i < len(raw); i++ {
			if raw[i] == '\\' {
				i++
				continue
			}
			if raw[i] == '"' {
				break
			}
		}
		var value string
		if json.Unmarshal(raw[start:i+1], &value) == nil && r.contains(value) {
			changed = true
		}
	}
	var walk func(any, int) (any, error)
	walk = func(value any, depth int) (any, error) {
		if depth > 64 {
			return nil, fmt.Errorf("JSON redaction depth exceeded")
		}
		switch v := value.(type) {
		case string:
			redacted := r.text(v)
			changed = changed || redacted != v
			return redacted, nil
		case json.Number:
			if r.contains(string(v)) {
				changed = true
				return "[credential redacted]", nil
			}
		case []any:
			for i, item := range v {
				next, err := walk(item, depth+1)
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
		case map[string]any:
			next := make(map[string]any, len(v))
			for key, item := range v {
				redacted := r.text(key)
				changed = changed || redacted != key
				if _, exists := next[redacted]; exists {
					return nil, fmt.Errorf("JSON redaction key collision")
				}
				entry, err := walk(item, depth+1)
				if err != nil {
					return nil, err
				}
				next[redacted] = entry
			}
			return next, nil
		}
		return value, nil
	}
	value, err := walk(value, 0)
	if err != nil {
		return nil, changed, err
	}
	if !changed {
		return slices.Clone(raw), false, nil
	}
	encoded, err := json.Marshal(value)
	if err == nil && r.contains(string(encoded)) {
		return nil, true, fmt.Errorf("JSON still contains a known credential")
	}
	return encoded, true, err
}
