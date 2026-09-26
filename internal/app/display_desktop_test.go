package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func desktopDisplayResult(p *desktopDisplayProjection, name, args, result string, failed bool) *interaction.DesktopDisplay {
	call := llm.ToolCall{Name: name, Arguments: json.RawMessage(args)}
	return p.end(agent.AgentEvent{ToolCall: &call, ToolResult: &llm.ToolResultMessage{
		IsError: failed, Content: []llm.ContentPart{{Type: llm.ContentTypeText, Text: result}},
	}})
}

func TestDesktopDisplayFollowsReturnedReferencesOnly(t *testing.T) {
	t.Parallel()
	var p desktopDisplayProjection
	desktopDisplayResult(&p, "desktop_apps", `{}`, `{"apps":[{"app_ref":"a","name":"Notes"}],"windows":[{"target_ref":"w","app":"Notes"}]}`, false)
	got := p.start(llm.ToolCall{Name: "desktop_observe", Arguments: []byte(`{"target_ref":"w"}`)})
	if got.App != "Notes" || got.Phase != "Observing" {
		t.Fatal(got)
	}
	desktopDisplayResult(&p, "desktop_observe", `{"target_ref":"w"}`, `{"observation_ref":"o","target_ref":"w"}`, false)
	args := `{"action":"type_text","observation_ref":"o","text":"PRIVATE INPUT"}`
	got = p.start(llm.ToolCall{Name: "desktop_act", Arguments: []byte(args)})
	if got.App != "Notes" || got.Phase != "Background requested" {
		t.Fatal(got)
	}
	got = desktopDisplayResult(&p, "desktop_act", args, `{"outcome":"returned","observation":{"observation_ref":"o2","target_ref":"w"}}`, false)
	if got.App != "Notes" || got.Phase != "Returned" {
		t.Fatal(got)
	}
	got = p.start(llm.ToolCall{Name: "desktop_act", Arguments: []byte(`{"action":"click","delivery_mode":"foreground","observation_ref":"o2"}`)})
	if got.App != "Notes" || got.Phase != "Foreground requested" {
		t.Fatal(got)
	}
	for _, args := range []string{`{"action":"click","observation_ref":"unseen","app":"FORGED"}`, `{"action":`} {
		got = p.start(llm.ToolCall{Name: "desktop_act", Arguments: []byte(args)})
		if got.App != "" {
			t.Fatal("guessed target", got)
		}
	}
	got = p.start(llm.ToolCall{Name: "desktop_act", Arguments: []byte(`{"action":"launch","app_ref":"a"}`)})
	if got.App != "Notes" || got.Phase != "Launch requested" {
		t.Fatal(got)
	}
	desktopDisplayResult(&p, "desktop_apps", `{}`, `{"windows":[]}`, false)
	if p.start(llm.ToolCall{Name: "desktop_act", Arguments: []byte(args)}).App != "" {
		t.Fatal("discovery retained old display references")
	}
	var nextRun desktopDisplayProjection
	if nextRun.start(llm.ToolCall{Name: "desktop_observe", Arguments: []byte(`{"target_ref":"w"}`)}).App != "" {
		t.Fatal("new run inherited target labels")
	}
}

func TestDesktopDisplayPreservesUncertainty(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		result, want string
		failed       bool
	}{
		{`{"outcome":"unknown","observation":{"observation_ref":"fresh"}}`, "Outcome unknown", true},
		{`{"code":"setup_required"}`, "Needs setup", true},
		{`{"code":"desktop_busy"}`, "Desktop busy", true},
		{`{"code":"platform_unavailable"}`, "Unavailable", true},
		{`{"outcome":"returned","observation_error":"failed"}`, "Observation failed", true},
		{`{"degraded":true}`, "Observation incomplete", true},
		{`{"wait_state":"unsatisfied"}`, "Condition unmet", true},
		{`{"wait_state":"unknown"}`, "Condition unknown", true},
		{`{"wait_state":"satisfied"}`, "Condition met", false},
		{`{"outcome":"returned","driver":{"effect":"partial"}}`, "Needs attention", true},
		{`setup_required success`, "Needs attention", true},
		{`{"driver":{"code":"setup_required"}}`, "Needs attention", true},
		{`{"padding":"` + strings.Repeat("x", 96*1024) + `","outcome":"unknown"}`, "Outcome unknown", true},
		{strings.Repeat("x", 512*1024+1), "Needs attention", true},
	} {
		var p desktopDisplayProjection
		if got := desktopDisplayResult(&p, "desktop_act", `{"action":"wait"}`, test.result, test.failed); got.Phase != test.want {
			t.Fatalf("got %v, want %s", got, test.want)
		}
	}
}

func TestDesktopDisplayBoundsNamesAndIgnoresOtherTools(t *testing.T) {
	t.Parallel()
	var p desktopDisplayProjection
	for i := range 300 {
		p.remember(&p.windows, strings.Repeat("r", i), strings.Repeat("界", 300))
		if len(p.windows) > 64 {
			t.Fatal("unbounded names")
		}
		for _, name := range p.windows {
			if len(name) > 256 {
				t.Fatal("unbounded label")
			}
		}
	}
	call := llm.ToolCall{Name: "read", Arguments: []byte(`{"path":"desktop_act"}`)}
	if p.start(call) != nil || p.end(agent.AgentEvent{ToolCall: &call}) != nil {
		t.Fatal("non-desktop tool acquired desktop presentation")
	}
}

func TestDesktopHistoryWithoutResultDoesNotClaimCompletion(t *testing.T) {
	t.Parallel()
	s := browserHarness(t)
	store := browserFixture(t, s, "desktop-incomplete", "", "", 100)
	call := llm.ToolCall{ID: "desktop-call", Name: "desktop_act", Arguments: []byte(`{"action":"click","observation_ref":"old"}`)}
	message := llm.AssistantMessage{Role: llm.RoleAssistant, API: "api", Provider: "provider", ModelID: "model", Timestamp: 101,
		StopReason: llm.StopReasonToolUse, Content: []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &call}}}
	if err := appendTestSessionMessages(t.Context(), store, []llm.AgentMessage{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("Synthetic desktop request").Part()}, Timestamp: 100}, message}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	view, err := sessionTranscript(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got := view.Entries[len(view.Entries)-1].Tool
	if got.Desktop == nil || got.Desktop.Phase != "Result not recorded" || !got.Failed {
		t.Fatal(got)
	}
}
