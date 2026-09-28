package llm

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// ExecutionState describes what is known about a tool invocation, not whether
// its requested business operation succeeded. Legacy results have no state.
type ExecutionState string

const (
	ExecutionNotDispatched ExecutionState = "not_dispatched"
	ExecutionReturned      ExecutionState = "returned"
	ExecutionUnknown       ExecutionState = "unknown"
)

// ToolBinding records application-bound provenance. It carries neither secrets
// nor live handles and never grants authority when a Session is resumed.
type ToolBinding struct {
	Source                string `json:"source"`
	ServiceID             string `json:"service_id"`
	ConnectionFingerprint string `json:"connection_fingerprint"`
	ToolName              string `json:"tool_name"`
	SchemaFingerprint     string `json:"schema_fingerprint"`
}

// MaxStructuredResultBytes is a storage bound, independent of model context
// trimming. Producers must report loss explicitly when they cannot retain data.
const MaxStructuredResultBytes = 1 << 20

// ToolResultDetails is optional provider-neutral source metadata. Structured
// content is JSON, kept independently of the ordered text/image content. Loss
// describes data never saved, not content omitted only from a model request.
type ToolResultDetails struct {
	State             ExecutionState  `json:"state"`
	Binding           *ToolBinding    `json:"binding,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	Loss              string          `json:"loss,omitempty"`
}

// Clone transfers mutable details to an independent owner.
func (d *ToolResultDetails) Clone() *ToolResultDetails {
	if d == nil {
		return nil
	}
	copy := *d
	copy.StructuredContent = slices.Clone(d.StructuredContent)
	if d.Binding != nil {
		binding := *d.Binding
		copy.Binding = &binding
	}
	return &copy
}

// Validate accepts absent legacy metadata and rejects ambiguous new states.
func (d *ToolResultDetails) Validate() error {
	if d == nil {
		return nil
	}
	switch d.State {
	case ExecutionNotDispatched, ExecutionReturned, ExecutionUnknown:
	default:
		return fmt.Errorf("tool result execution state %q is invalid", d.State)
	}
	if len(d.StructuredContent) > 0 {
		if !json.Valid(d.StructuredContent) || !utf8.Valid(d.StructuredContent) {
			return fmt.Errorf("tool result structured content must be valid UTF-8 JSON")
		}
		if len(d.StructuredContent) > MaxStructuredResultBytes {
			return fmt.Errorf("tool result structured content exceeds %d bytes", MaxStructuredResultBytes)
		}
	}
	if !utf8.ValidString(d.Loss) || len(d.Loss) > 4096 {
		return fmt.Errorf("tool result loss notice must be UTF-8 and at most 4096 bytes")
	}
	if d.Binding != nil {
		for _, field := range []struct{ name, value string }{
			{"source", d.Binding.Source},
			{"service ID", d.Binding.ServiceID},
			{"connection fingerprint", d.Binding.ConnectionFingerprint},
			{"tool name", d.Binding.ToolName},
			{"schema fingerprint", d.Binding.SchemaFingerprint},
		} {
			if strings.TrimSpace(field.value) == "" || !utf8.ValidString(field.value) ||
				len(field.value) > 4096 || strings.ContainsAny(field.value, "\x00\r\n") {
				return fmt.Errorf("tool result binding %s is invalid", field.name)
			}
		}
	}
	return nil
}

// ToolResultModelContent projects a result without mutating source history.
// Adapters keep the original block order and append JSON/status as text when
// their protocol has no equivalent structured result field. Binding identity
// remains source metadata and cannot become an instruction or authorization.
func ToolResultModelContent(content []ContentPart, details *ToolResultDetails, isError bool) []ContentPart {
	if details == nil {
		return content
	}
	projected := slices.Clone(content)
	if len(details.StructuredContent) > 0 {
		projected = append(projected, NewTextContent("Structured result (JSON):\n"+string(details.StructuredContent)).Part())
	}
	if isError {
		projected = append(projected, NewTextContent("Tool reported an error; returned content may be partial.").Part())
	}
	switch details.State {
	case ExecutionNotDispatched:
		projected = append(projected, NewTextContent("Execution was not dispatched.").Part())
	case ExecutionUnknown:
		projected = append(projected, NewTextContent("Execution outcome unknown; the tool may have produced effects. Inspect current state before deciding whether to retry. Cancellation does not undo external effects.").Part())
	}
	if details.Loss != "" {
		projected = append(projected, NewTextContent("Result data was not saved and cannot be recovered: "+details.Loss).Part())
	}
	return projected
}
