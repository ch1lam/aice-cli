package app

import (
	"encoding/json"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Only application-assigned model names identify the managed instance. An
// ordinary service with the same remote operation name has a different name.
// Display recognition is not execution authority or a native target registry.
func managedDesktopOperation(name string) string {
	for _, operation := range managedCUAToolNames() {
		if name == mcpModelName(managedCUAKey, operation) {
			return operation
		}
	}
	return ""
}

func managedDesktopStart(operation string, raw json.RawMessage) *interaction.DesktopDisplay {
	d := &interaction.DesktopDisplay{Phase: "Preparing"}
	var args struct {
		DeliveryMode string `json:"delivery_mode"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return d
	}
	switch operation {
	case "list_apps", "list_windows":
		d.Phase = "Discovering"
	case "get_window_state":
		d.Phase = "Observing"
	case "launch_app":
		d.Phase = "Launch requested"
	case "set_value":
		d.Phase = "Value change requested"
	default:
		if args.DeliveryMode == "foreground" {
			d.Phase = "Foreground requested"
		} else if args.DeliveryMode == "" || args.DeliveryMode == "background" {
			d.Phase = "Background requested"
		}
	}
	return d
}

func managedDesktopEnd(operation string, event agent.AgentEvent) *interaction.DesktopDisplay {
	d := managedDesktopStart(operation, event.ToolCall.Arguments)
	d.Phase = "Needs attention"
	result := event.ToolResult
	if result == nil || result.Details == nil || result.Details.Binding == nil {
		return d
	}
	binding := result.Details.Binding
	if binding.Source != "managed:computer-use" || binding.ServiceID != "cua" || binding.ToolName != operation {
		return d
	}
	switch result.Details.State {
	case llm.ExecutionUnknown:
		d.Phase = "Outcome unknown"
	case llm.ExecutionNotDispatched:
		d.Phase = "Not dispatched"
	case llm.ExecutionReturned:
		if !result.IsError && event.Err == nil {
			d.Phase = "Returned"
		}
	}
	return d
}
