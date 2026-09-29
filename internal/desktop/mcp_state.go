package desktop

import (
	"encoding/json"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// Session lifetime belongs to the host connection owner. Driver 0.29.1 can
// report expiry as a domain result while the MCP transport remains healthy:
// core/tool.rs uses refusal.code; the macOS daemon's serve.rs wraps an exact
// pre-dispatch rejection as tool_invocation_failed. Retiring that connection
// permits the next explicit discovery to establish both native lifecycles.
// This recognizes expiry only; ordinary action failures/degradation pass through.
func managedSessionEnded(name string, result mcpclient.Result) bool {
	if !result.IsError {
		return false
	}
	var state struct {
		Code    string `json:"code"`
		Refusal struct {
			Code string `json:"code"`
		} `json:"refusal"`
	}
	if len(result.StructuredContent) > 0 && json.Unmarshal(result.StructuredContent, &state) != nil {
		return false
	}
	if state.Code == "session_ended" || state.Refusal.Code == "session_ended" {
		return true
	}
	if state.Code != "" && state.Code != "tool_invocation_failed" {
		return false
	}
	suffix := "' has ended; tool call '" + name + "' was rejected. Call start_session with this id to revive it before issuing further actions, or use a new session id."
	for _, block := range result.Content {
		if block.Kind != mcpclient.BlockText {
			continue
		}
		label, ok := strings.CutPrefix(block.Text, "session '")
		if !ok {
			continue
		}
		label, ok = strings.CutSuffix(label, suffix)
		if ok && label != "" && !strings.ContainsAny(label, "'\r\n") {
			return true
		}
	}
	return false
}

// Local policy errors are safe to show. Native errors and results pass through
// the generic MCP result mapper without a CUA-specific interpretation.
func managedReject(err error) (mcpclient.Result, error) {
	return mcpclient.Result{State: llm.ExecutionNotDispatched, IsError: true,
		Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "AICE CUA policy: " + err.Error()}}}, err
}
