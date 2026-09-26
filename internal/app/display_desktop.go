package app

import (
	"encoding/json"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// A run-local display projection, populated only by recorded tool results.
// Names are hints for presentation, not a second executable reference registry.
// The event bridge is serial; no native I/O or synchronization is needed here.
type desktopDisplayProjection struct {
	apps, windows, observations map[string]string
}

func desktopTool(name string) bool {
	return name == "desktop_apps" || name == "desktop_observe" || name == "desktop_act"
}

func (p *desktopDisplayProjection) start(call llm.ToolCall) *interaction.DesktopDisplay {
	if !desktopTool(call.Name) {
		return nil
	}
	// Separate wire fields keep this projection independent of input validation.
	var args struct {
		Action         string `json:"action"`
		DeliveryMode   string `json:"delivery_mode"`
		AppRef         string `json:"app_ref"`
		TargetRef      string `json:"target_ref"`
		ObservationRef string `json:"observation_ref"`
	}
	d := &interaction.DesktopDisplay{Phase: "Preparing"}
	if json.Unmarshal(call.Arguments, &args) != nil {
		return d
	}
	switch call.Name {
	case "desktop_apps":
		d.Phase = "Discovering"
	case "desktop_observe":
		d.App, d.Phase = p.windows[args.TargetRef], "Observing"
	case "desktop_act":
		d.App = p.observations[args.ObservationRef]
		switch args.Action {
		case "launch":
			d.App, d.Phase = p.apps[args.AppRef], "Launch requested"
		case "wait":
			d.Phase = "Waiting"
		case "set_value":
			d.Phase = "Value change requested"
		case "click", "double_click", "right_click", "type_text", "key", "hotkey", "scroll", "drag":
			if args.DeliveryMode == "foreground" {
				d.Phase = "Foreground requested"
			} else if args.DeliveryMode == "" || args.DeliveryMode == "background" {
				d.Phase = "Background requested"
			}
		}
	}
	return d
}

type desktopObservationDisplay struct {
	Ref       string `json:"observation_ref"`
	TargetRef string `json:"target_ref"`
}

func (p *desktopDisplayProjection) end(event agent.AgentEvent) *interaction.DesktopDisplay {
	if event.ToolCall == nil {
		return nil
	}
	call := *event.ToolCall
	d := p.start(call)
	if d == nil {
		return nil
	}
	d.Phase = "Needs attention"
	if event.ToolResult == nil {
		return d
	}
	var raw string
	for _, part := range event.ToolResult.Content {
		if part.Type == llm.ContentTypeText {
			raw = part.Text
			break
		}
	}
	// AICE's desktop adapter emits one bounded JSON object. Do not infer facts
	// from free-form errors, Driver prose, images or truncated display output.
	var result struct {
		desktopObservationDisplay
		Apps             []desktop.Application      `json:"apps"`
		Windows          []desktop.Window           `json:"windows"`
		Observation      *desktopObservationDisplay `json:"observation"`
		Code             string                     `json:"code"`
		Outcome          string                     `json:"outcome"`
		WaitState        string                     `json:"wait_state"`
		ObservationError string                     `json:"observation_error"`
		Degraded         bool                       `json:"degraded"`
	}
	if raw == "" || len(raw) > 512*1024 || json.Unmarshal([]byte(raw), &result) != nil {
		return d
	}
	if call.Name == "desktop_apps" {
		p.apps, p.windows, p.observations = nil, nil, nil
		for _, app := range result.Apps[:min(len(result.Apps), 64)] {
			p.remember(&p.apps, app.Ref, app.Name)
		}
	}
	for _, window := range result.Windows[:min(len(result.Windows), 64)] {
		p.remember(&p.windows, window.Ref, window.App)
	}
	obs := result.Observation
	if call.Name == "desktop_observe" {
		obs = &result.desktopObservationDisplay
	}
	if obs != nil {
		name := p.windows[obs.TargetRef]
		p.remember(&p.observations, obs.Ref, name)
		if name != "" {
			d.App = name
		}
	}
	switch {
	case result.Outcome == "unknown":
		d.Phase = "Outcome unknown"
	case result.Code == "setup_required":
		d.Phase = "Needs setup"
	case result.Code == "platform_unavailable":
		d.Phase = "Unavailable"
	case result.Code == "desktop_busy":
		d.Phase = "Desktop busy"
	case result.ObservationError != "":
		d.Phase = "Observation failed"
	case result.Degraded:
		d.Phase = "Observation incomplete"
	case result.WaitState == "unsatisfied":
		d.Phase = "Condition unmet"
	case result.WaitState == "unknown":
		d.Phase = "Condition unknown"
	case event.Err != nil || event.ToolResult.IsError:
		d.Phase = "Needs attention"
	case result.WaitState == "satisfied":
		d.Phase = "Condition met"
	default:
		d.Phase = "Returned"
	}
	return d
}

func (*desktopDisplayProjection) remember(names *map[string]string, ref, name string) {
	if ref == "" || len(ref) > 256 || name == "" {
		return
	}
	if *names == nil || len(*names) >= 64 {
		*names = make(map[string]string)
	}
	// UTF-8 names and terminal escaping remain the frontend's responsibility.
	(*names)[ref] = strings.Clone(strings.ToValidUTF8(name[:min(len(name), 256)], ""))
}

func (p *desktopDisplayProjection) decorate(event agent.AgentEvent, display *interaction.Event) {
	if display == nil || event.ToolCall == nil {
		return
	}
	switch display.Kind {
	case interaction.EventToolStart:
		display.Tool.Desktop = p.start(*event.ToolCall)
	case interaction.EventToolEnd:
		display.Tool.Desktop = p.end(event)
	}
}
