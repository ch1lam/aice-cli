//go:build integration && (darwin || linux)

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"runtime"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type nativeManagedCUAModel struct {
	readback                            nativeManagedReadback
	viewBudget                          int64
	inputActions                        map[int]string
	results                             []llm.ToolResultMessage
	images                              int
	targets                             []nativePrintFixture
	names                               map[string]string
	index, phase, requests, nativeCalls int
	loaded                              bool
	pending, pendingID                  string
	window                              uint64
	elements                            []desktop.Element
}

func (m *nativeManagedCUAModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.requests++
	m.viewBudget = llm.ResultViewBudget(request.Model.ContextWindow)
	if m.pending != "" {
		result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
		if !ok || result.IsError {
			return nil, fmt.Errorf("managed comparison result failed at %s", m.pending)
		}
		m.results = append(m.results, result)
		result, stream, err := m.readback.consume(request.Model, result)
		if stream != nil || err != nil {
			return stream, err
		}
		if result.ToolCallID != m.pendingID {
			return nil, fmt.Errorf("unexpected managed result identity")
		}
		switch m.pending {
		case "skill":
			if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "**"+desktop.DriverVersion+"**") {
				return nil, fmt.Errorf("missing version-matched Skill")
			}
			m.loaded = true
		case "tool_search":
			var found tool.ToolSearchResult
			if len(result.Content) == 0 || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil {
				return nil, fmt.Errorf("missing tool search result")
			}
			for _, entry := range found.Entries {
				if entry.Service == managedCUAKey {
					m.names[strings.TrimPrefix(entry.ID, managedCUAKey+"/tool/")] = entry.Name
				}
			}
		default:
			if result.Details == nil || result.Details.Binding == nil || result.Details.Binding.ServiceID != "cua" || result.Details.Binding.ToolName != m.pending || result.Details.State != llm.ExecutionReturned {
				return nil, fmt.Errorf("missing managed operation provenance")
			}
			m.nativeCalls++
			switch m.pending {
			case "list_windows":
				var found struct {
					Windows []struct {
						PID    int    `json:"pid"`
						Window uint64 `json:"window_id"`
						Title  string `json:"title"`
					} `json:"windows"`
				}
				if json.Unmarshal(nativeManagedJSON(result), &found) != nil {
					return nil, fmt.Errorf("invalid native windows")
				}
				m.window = 0
				for _, window := range found.Windows {
					if window.PID == m.targets[m.index].pid && window.Title == m.targets[m.index].name {
						m.window = window.Window
					}
				}
				if m.window == 0 {
					return nil, fmt.Errorf("synthetic window not discovered")
				}
			case "get_window_state":
				var observed struct {
					PID      int               `json:"pid"`
					Window   uint64            `json:"window_id"`
					Elements []desktop.Element `json:"elements"`
				}
				if json.Unmarshal(nativeManagedJSON(result), &observed) != nil || observed.PID != m.targets[m.index].pid || observed.Window != m.window {
					return nil, fmt.Errorf("native observation target mismatch: json_bytes=%d pid=%d/%d window=%d/%d parts=%d", len(nativeManagedJSON(result)), observed.PID, m.targets[m.index].pid, observed.Window, m.window, len(result.Content))
				}
				m.elements = observed.Elements
				images := 0
				for _, part := range result.Content {
					if part.Image != nil {
						size, err := png.DecodeConfig(bytes.NewReader(part.Image.Data))
						if err != nil || size.Width <= 0 || size.Height <= 0 {
							return nil, fmt.Errorf("invalid delivered native image")
						}
						images++
					}
				}
				m.images += images
				if images != 1 {
					return nil, fmt.Errorf("expected one current native screenshot")
				}
			}
			m.phase++
			if m.phase == 6 {
				m.index++
				m.phase = 0
			}
		}
		m.pending = ""
	}
	call := func(operation, name string, args any) (llm.Stream, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		m.pending, m.pendingID = operation, fmt.Sprintf("managed-%d", m.requests)
		return toolCallEventStream(request.Model, llm.ToolCall{ID: m.pendingID, Name: name, Arguments: raw}), nil
	}
	if !m.loaded {
		return call("skill", "skill", map[string]any{"name": "computer-use"})
	}
	if m.index == len(m.targets) {
		return (&recordingModel{response: "Managed synthetic sequence finished."}).Stream(ctx, request)
	}
	operation := []string{"list_windows", "get_window_state", "set_value", "get_window_state", "click", "get_window_state"}[m.phase]
	if m.phase == 2 && m.inputActions[m.index] != "" {
		operation = m.inputActions[m.index]
	}
	name := m.names[operation]
	if name == "" || !slices.ContainsFunc(request.Tools, func(definition llm.ToolDefinition) bool { return definition.Name == name }) {
		return call("tool_search", "tool_search", tool.ToolSearchRequest{Service: managedCUAKey, Query: operation, Limit: 1})
	}
	args := map[string]any{"pid": m.targets[m.index].pid}
	if operation != "list_windows" {
		args["window_id"] = m.window
	}
	if operation == "get_window_state" {
		args["include_screenshot"] = true
	}
	if m.phase == 2 || m.phase == 4 {
		label, role := "Task value", "AXTextField"
		if m.phase == 4 {
			label, role = "Commit", "AXButton"
		}
		token := ""
		for _, element := range m.elements {
			if element.Label == label && (runtime.GOOS != "darwin" || element.Role == role) && element.Token != "" {
				if token != "" {
					return nil, fmt.Errorf("ambiguous synthetic element")
				}
				token = element.Token
			}
		}
		if token == "" {
			return nil, fmt.Errorf("synthetic semantic target missing")
		}
		args["element_token"] = token
		if operation == "set_value" {
			args["value"] = nativePrintValue(m.index)
		}
		if operation == "type_text" {
			args["text"] = nativePrintValue(m.index)
		}
	}
	return call(operation, name, args)
}

// The model view may omit duplicated structured JSON while retaining the
// Driver's complete first text block. Use only complete JSON delivered in that
// request; never reach into Session storage from the scripted model.
func nativeManagedJSON(result llm.ToolResultMessage) json.RawMessage {
	if result.Details != nil && len(result.Details.StructuredContent) > 0 {
		return result.Details.StructuredContent
	}
	for _, part := range result.Content {
		if part.Type == llm.ContentTypeText && json.Valid([]byte(part.Text)) {
			return json.RawMessage(part.Text)
		}
	}
	return nil
}

// Native scripts use the public readback tool when the ordinary request budget
// omits JSON. Reassembly is local model state, not a second Session reader.
type nativeManagedReadback struct {
	source  *llm.ToolResultMessage
	data    []byte
	pending string
	calls   int
}

func (r *nativeManagedReadback) consume(model llm.Model, result llm.ToolResultMessage) (llm.ToolResultMessage, llm.Stream, error) {
	if r.source != nil {
		var page struct {
			Offset     int  `json:"offset"`
			TotalBytes int  `json:"total_bytes"`
			Complete   bool `json:"complete"`
			NextOffset int  `json:"next_offset"`
		}
		if result.ToolCallID != r.pending || result.IsError || len(result.Content) != 2 || json.Unmarshal([]byte(result.Content[0].Text), &page) != nil {
			return result, nil, fmt.Errorf("invalid native result readback")
		}
		if page.Offset != len(r.data) || page.TotalBytes > 256<<10 || page.TotalBytes <= 0 {
			return result, nil, fmt.Errorf("invalid native JSON page bounds")
		}
		r.data = append(r.data, result.Content[1].Text...)
		if page.Complete {
			if len(r.data) != page.TotalBytes || !json.Valid(r.data) {
				return result, nil, fmt.Errorf("incomplete native JSON readback")
			}
			assembled := *r.source
			assembled.Details = assembled.Details.Clone()
			assembled.Details.StructuredContent = append([]byte(nil), r.data...)
			r.source, r.data, r.pending = nil, nil, ""
			return assembled, nil, nil
		}
		if page.NextOffset != len(r.data) || page.NextOffset <= page.Offset {
			return result, nil, fmt.Errorf("native JSON page made no progress")
		}
	} else {
		if result.Details == nil || result.Details.Binding == nil || nativeManagedJSON(result) != nil {
			return result, nil, nil
		}
		r.source = &result
	}
	r.calls++
	r.pending = fmt.Sprintf("%s-read-%d", r.source.ToolCallID, len(r.data))
	args, _ := json.Marshal(map[string]any{"call_id": r.source.ToolCallID, "section": "structured", "offset": len(r.data), "length": 8192})
	return result, toolCallEventStream(model, llm.ToolCall{ID: r.pending, Name: "tool_result_read", Arguments: args}), nil
}
