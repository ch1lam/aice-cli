package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestModelSubmitsPromptAndConsumesAgentEvents(t *testing.T) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	controllerDone := make(chan struct{})
	current := newModel(requests, controllerDone)
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("inspect this repository")

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !handled || command == nil {
		t.Fatal("enter did not submit the prompt")
	}
	if !updated.running || updated.input.Focused() {
		t.Fatal("model did not enter running state and blur input")
	}

	rawStartMessage := command()
	startMessage, ok := rawStartMessage.(runStartedMsg)
	if !ok {
		t.Fatalf("start command message = %T, want runStartedMsg", rawStartMessage)
	}
	request := <-requests
	if request.prompt != "inspect this repository" {
		t.Errorf("run prompt = %q, want inspect this repository", request.prompt)
	}
	updated = updateModel(t, updated, startMessage)

	identity := llm.Model{ID: "test", API: "test", Provider: "test"}
	startedAssistant := llm.NewAssistantMessage(identity)
	completedAssistant := llm.NewAssistantMessage(identity)
	completedAssistant.Content = []llm.ContentPart{
		llm.NewThinkingContent("checking context", "").Part(),
		llm.NewTextContent("**Inspection complete.**").Part(),
	}
	completedAssistant.StopReason = llm.StopReasonStop
	call := llm.ToolCall{ID: "call-1", Name: "read", Arguments: []byte(`{"path":"go.mod"}`)}
	toolResult := llm.ToolResultMessage{
		Role:       llm.RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    []llm.ContentPart{llm.NewTextContent("module contents").Part()},
	}

	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{
		{cancel: func() {}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageStart,
			Message: startedAssistant,
		}},
		{event: agent.AgentEvent{
			Type: agent.EventTypeMessageUpdate,
			AssistantMessageEvent: &llm.Event{
				Type:  llm.EventTypeTextDelta,
				Delta: "Inspection",
			},
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageEnd,
			Message: completedAssistant,
		}},
		{event: agent.AgentEvent{
			Type:     agent.EventTypeToolExecutionStart,
			ToolCall: &call,
		}},
		{event: agent.AgentEvent{
			Type:     agent.EventTypeToolExecutionEnd,
			ToolCall: &call,
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageStart,
			Message: toolResult,
		}},
		{event: agent.AgentEvent{
			Type:    agent.EventTypeMessageEnd,
			Message: toolResult,
		}},
		{event: agent.AgentEvent{
			Type: agent.EventTypeAgentEnd,
			Messages: []llm.AgentMessage{
				completedAssistant,
				toolResult,
			},
		}},
		{done: true},
	}})

	if updated.running {
		t.Fatal("model remains running after terminal update")
	}
	if !updated.input.Focused() {
		t.Fatal("input is not focused after terminal update")
	}
	if updated.cancelRun != nil {
		t.Fatal("run cancellation function remains after completion")
	}
	if len(updated.entries) != 3 {
		t.Fatalf("transcript entries = %#v, want user, assistant, and tool", updated.entries)
	}
	transcript := updated.transcriptView()
	for _, want := range []string{
		"inspect this repository",
		"checking context",
		"Inspection complete.",
		"read",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript does not contain %q: %q", want, transcript)
		}
	}
}

func TestModelCollapsesProcessWhenConclusionStartsStreaming(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.entries = append(
		current.entries,
		transcriptEntry{kind: entryUser, text: "inspect"},
	)
	current.running = true
	processID := current.beginProcess()

	identity := llm.Model{ID: "test", API: "test", Provider: "test"}
	intermediate := llm.NewAssistantMessage(identity)
	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageStart,
		Message: intermediate,
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeThinkingDelta,
			Delta: "INTERMEDIATE_REASONING",
		},
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeTextDelta,
			Delta: "MIDDLEOUTPUT",
		},
	})
	if group := current.processGroup(processID); group == nil || !group.collapsed {
		t.Fatal("text delta did not provisionally collapse the process")
	}

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: []byte(`{"path":"go.mod"}`),
	}
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type: llm.EventTypeToolCallStart,
		},
	})
	if group := current.processGroup(processID); group == nil || group.collapsed {
		t.Fatal("tool call did not reopen a provisionally collapsed process")
	}
	intermediate.Content = []llm.ContentPart{
		llm.NewThinkingContent("INTERMEDIATE_REASONING", "").Part(),
		llm.NewTextContent("MIDDLEOUTPUT").Part(),
		{Type: llm.ContentTypeToolCall, ToolCall: &call},
	}
	intermediate.StopReason = llm.StopReasonToolUse
	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: intermediate,
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &call,
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolCall: &call,
	})

	conclusion := llm.NewAssistantMessage(identity)
	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageStart,
		Message: conclusion,
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeThinkingDelta,
			Delta: "FINAL_REASONING",
		},
	})

	beforeConclusion := ansi.Strip(current.transcriptView())
	for _, want := range []string{
		"INTERMEDIATE_REASONING",
		"MIDDLEOUTPUT",
		"read",
		"FINAL_REASONING",
	} {
		if !strings.Contains(beforeConclusion, want) {
			t.Fatalf(
				"expanded process before conclusion = %q, want %q",
				beforeConclusion,
				want,
			)
		}
	}

	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeTextDelta,
			Delta: " \n",
		},
	})
	if group := current.processGroup(processID); group == nil || group.collapsed {
		t.Fatal("leading whitespace collapsed the process before final output")
	}
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeTextDelta,
			Delta: "FINAL_ANSWER",
		},
	})

	collapsed := ansi.Strip(current.transcriptView())
	for _, hidden := range []string{
		"INTERMEDIATE_REASONING",
		"MIDDLEOUTPUT",
		"read",
		"go.mod",
		"FINAL_REASONING",
	} {
		if strings.Contains(collapsed, hidden) {
			t.Errorf("collapsed process still contains %q: %q", hidden, collapsed)
		}
	}
	for _, want := range []string{
		"▶ PROCESS",
		"1 tool call",
		"ctrl+o to expand",
		"FINAL_ANSWER",
	} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("collapsed transcript = %q, want %q", collapsed, want)
		}
	}

	expanded, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'o',
		Mod:  tea.ModCtrl,
	}))
	if !handled || command != nil {
		t.Fatal("ctrl+o did not expand the process")
	}
	for _, want := range []string{
		"▼ PROCESS",
		"INTERMEDIATE_REASONING",
		"MIDDLEOUTPUT",
		"read",
		"FINAL_REASONING",
		"FINAL_ANSWER",
		"ctrl+o to collapse",
	} {
		if transcript := ansi.Strip(expanded.transcriptView()); !strings.Contains(
			transcript,
			want,
		) {
			t.Errorf("expanded transcript = %q, want %q", transcript, want)
		}
	}
}

func TestModelProcessSpacingKeepsToolsTogether(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	processID := current.beginProcess()
	current.entries = []transcriptEntry{
		{
			kind:      entryAssistant,
			text:      "MIDDLETEXT",
			complete:  true,
			processID: processID,
		},
		{
			kind:      entryTool,
			processID: processID,
			toolName:  "FIRSTTOOL",
			toolDone:  true,
		},
		{
			kind:      entryTool,
			processID: processID,
			toolName:  "SECONDTOOL",
			toolDone:  true,
		},
		{
			kind:       entryAssistant,
			thinking:   "FINALREASON",
			text:       "FINALANSWER",
			complete:   true,
			processID:  processID,
			conclusion: true,
		},
	}

	transcript := current.transcriptView()
	assertTranscriptGap(
		t,
		transcript,
		"MIDDLETEXT",
		"FIRSTTOOL",
		2,
	)
	assertTranscriptGap(
		t,
		transcript,
		"FIRSTTOOL",
		"SECONDTOOL",
		1,
	)
	assertTranscriptGap(
		t,
		transcript,
		"SECONDTOOL",
		"FINALREASON",
		2,
	)

	current.width = minimumWidth
	narrowHeader := current.processHeader(0, len(current.entries), true)
	if got := lipgloss.Width(narrowHeader); got > current.contentWidth() {
		t.Errorf(
			"narrow process header width = %d, want at most %d: %q",
			got,
			current.contentWidth(),
			narrowHeader,
		)
	}
	if !strings.Contains(narrowHeader, "ctrl+o to expand") {
		t.Errorf("narrow process header is missing expand hint: %q", narrowHeader)
	}
}

func TestModelAssistantBodyIsSeparatedAndUniformlyIndented(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 48, Height: 24})
	markdown := "First paragraph wraps onto another line so alignment stays visible.\n\n" +
		"---\n\n" +
		"## Heading\n\nSecond paragraph."
	tests := []struct {
		name  string
		entry transcriptEntry
	}{
		{
			name: "streaming markdown",
			entry: transcriptEntry{
				kind: entryAssistant,
				text: markdown,
			},
		},
		{
			name: "completed markdown",
			entry: transcriptEntry{
				kind:     entryAssistant,
				text:     markdown,
				rendered: renderMarkdown(markdown, current.contentWidth()),
				complete: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Strip(current.entryView(tt.entry, false))
			lines := strings.Split(got, "\n")
			for index := range lines {
				lines[index] = strings.TrimRight(lines[index], " ")
			}
			got = strings.Join(lines, "\n")
			if len(lines) < 3 || lines[0] != " ✦ AICE" || lines[1] != "" {
				t.Fatalf(
					"assistant heading is not separated from its body "+
						"by one blank line:\n%q",
					got,
				)
			}

			for index, line := range lines[2:] {
				if strings.TrimSpace(line) == "" {
					continue
				}
				if !strings.HasPrefix(line, "   ") {
					t.Errorf(
						"assistant body line %d is not uniformly indented: "+
							"%q\nfull view:\n%q",
						index+3,
						line,
						got,
					)
				}
			}
			if strings.Contains(got, "\n\n\n") {
				t.Errorf("assistant body contains excessive blank lines:\n%q", got)
			}
		})
	}
}

func TestModelWelcomeGuidesUnconfiguredLogin(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})

	welcome := current.welcomeView()
	for _, want := range []string{
		"No DeepSeek API key is configured",
		"/login",
		"/settings",
	} {
		if !strings.Contains(welcome, want) {
			t.Errorf("welcome = %q, want %q", welcome, want)
		}
	}
}

func TestModelViewEnablesMouseWheelEvents(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))

	if got := current.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("view mouse mode = %v, want cell motion for mouse wheel events", got)
	}
}

func TestModelMouseWheelScrollsTranscript(t *testing.T) {
	t.Parallel()

	current := newScrollableModel(t)
	initialOffset := current.viewport.YOffset()

	updated := updateModel(t, current, tea.MouseWheelMsg(tea.Mouse{
		Button: tea.MouseWheelDown,
	}))

	if got, want := updated.viewport.YOffset(), initialOffset+current.viewport.MouseWheelDelta; got != want {
		t.Errorf("viewport Y offset = %d, want %d after mouse wheel down", got, want)
	}
}

func TestModelFocusedInputKeysDoNotScrollTranscript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       tea.KeyPressMsg
		wantInput string
	}{
		{name: "j", key: tea.KeyPressMsg{Code: 'j', Text: "j"}, wantInput: "prefixj"},
		{name: "k", key: tea.KeyPressMsg{Code: 'k', Text: "k"}, wantInput: "prefixk"},
		{name: "f", key: tea.KeyPressMsg{Code: 'f', Text: "f"}, wantInput: "prefixf"},
		{name: "b", key: tea.KeyPressMsg{Code: 'b', Text: "b"}, wantInput: "prefixb"},
		{name: "u", key: tea.KeyPressMsg{Code: 'u', Text: "u"}, wantInput: "prefixu"},
		{name: "d", key: tea.KeyPressMsg{Code: 'd', Text: "d"}, wantInput: "prefixd"},
		{name: "space", key: tea.KeyPressMsg{Code: ' ', Text: " "}, wantInput: "prefix "},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}, wantInput: "prefix"},
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}, wantInput: "prefix"},
		{
			name:      "control u",
			key:       tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl},
			wantInput: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newScrollableModel(t)
			current.input.SetValue("prefix")
			initialOffset := current.viewport.YOffset()

			updated := updateModel(t, current, tt.key)

			if got := updated.viewport.YOffset(); got != initialOffset {
				t.Errorf("viewport Y offset = %d, want %d", got, initialOffset)
			}
			if got := updated.input.Value(); got != tt.wantInput {
				t.Errorf("input value = %q, want %q", got, tt.wantInput)
			}
		})
	}
}

func TestModelViewportAcceptsOnlyPublishedKeyboardScrollKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        tea.KeyPressMsg
		wantScroll bool
	}{
		{name: "j", key: tea.KeyPressMsg{Code: 'j', Text: "j"}},
		{name: "k", key: tea.KeyPressMsg{Code: 'k', Text: "k"}},
		{name: "f", key: tea.KeyPressMsg{Code: 'f', Text: "f"}},
		{name: "b", key: tea.KeyPressMsg{Code: 'b', Text: "b"}},
		{name: "u", key: tea.KeyPressMsg{Code: 'u', Text: "u"}},
		{name: "d", key: tea.KeyPressMsg{Code: 'd', Text: "d"}},
		{name: "space", key: tea.KeyPressMsg{Code: ' ', Text: " "}},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}},
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}},
		{name: "page up", key: tea.KeyPressMsg{Code: tea.KeyPgUp}, wantScroll: true},
		{name: "page down", key: tea.KeyPressMsg{Code: tea.KeyPgDown}, wantScroll: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newScrollableModel(t)
			current.running = true
			current.input.Blur()
			initialOffset := current.viewport.YOffset()

			updated := updateModel(t, current, tt.key)

			scrolled := updated.viewport.YOffset() != initialOffset
			if scrolled != tt.wantScroll {
				t.Errorf(
					"viewport Y offset = %d, initial %d, want scroll %v",
					updated.viewport.YOffset(),
					initialOffset,
					tt.wantScroll,
				)
			}
		})
	}
}

func TestModelControlCCancelsOnlyActiveRun(t *testing.T) {
	t.Parallel()

	cancelled := false
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.cancelRun = func() { cancelled = true }

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'c',
		Mod:  tea.ModCtrl,
	}))
	if !handled || command != nil {
		t.Fatal("ctrl+c should cancel an active run without quitting")
	}
	if !cancelled {
		t.Fatal("ctrl+c did not invoke active run cancellation")
	}
	if updated.status != "Cancelling current response..." {
		t.Errorf("status = %q, want cancellation status", updated.status)
	}
}

func TestModelControlCBeforeRunStartsDefersCancellation(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'c',
		Mod:  tea.ModCtrl,
	}))
	if !handled || command != nil {
		t.Fatal("ctrl+c should defer cancellation while a run is starting")
	}
	if !updated.cancelRequested {
		t.Fatal("model did not remember cancellation requested before run start")
	}

	cancelled := false
	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{
		{cancel: func() { cancelled = true }},
	}})
	if !cancelled {
		t.Fatal("deferred cancellation did not cancel the started run")
	}
}

func TestModelHelpTogglesAndUsesAvailableHeight(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	collapsedHeight := current.viewport.Height()

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyF1}))
	if !handled || command != nil {
		t.Fatal("f1 did not toggle help")
	}
	if !updated.help.ShowAll {
		t.Fatal("help remains collapsed after f1")
	}
	if updated.viewport.Height() >= collapsedHeight {
		t.Errorf(
			"expanded help viewport height = %d, want less than %d",
			updated.viewport.Height(),
			collapsedHeight,
		)
	}
	if !updated.viewport.AtBottom() {
		t.Fatal("expanded help left the empty welcome viewport scrollable")
	}
}

func TestModelKeepsReadyInHeaderAndUsesBubblesHelpBelowComposer(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("composer marker")
	current.input.Blur()

	content := current.View().Content
	composerIndex := strings.Index(content, "composer marker")
	helpView := current.help.View(current.keys.forState(false))
	helpIndex := strings.Index(content, helpView)
	if composerIndex < 0 || helpView == "" || helpIndex < 0 {
		t.Fatalf(
			"view is missing composer or Bubbles help: composer=%d help=%d",
			composerIndex,
			helpIndex,
		)
	}
	if composerIndex >= helpIndex {
		t.Fatalf(
			"composer is not above help: composer=%d help=%d",
			composerIndex,
			helpIndex,
		)
	}
	if footer := current.footerView(80); strings.Contains(footer, current.status) {
		t.Fatalf("footer repeats ready status from header: %q", footer)
	}
	if got := strings.Count(content, "READY"); got != 1 {
		t.Fatalf("READY count = %d, want header only:\n%s", got, content)
	}
}

func TestModelHeaderShowsShellWorkingDirectory(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	originalViewportHeight := current.viewport.Height()
	current.workingDirectory = filepath.Join(
		string(filepath.Separator),
		"workspace",
		"projects",
		"coding-agents",
		"aice-cli",
	)
	current.resizeLayout()

	fullHeader := current.headerView(80)
	for _, want := range []string{"AICE", "aice-cli", "READY"} {
		if !strings.Contains(fullHeader, want) {
			t.Errorf("header = %q, want %q", fullHeader, want)
		}
	}
	if strings.Contains(current.footerView(80), "aice-cli") {
		t.Fatalf(
			"footer still contains working directory: %q",
			current.footerView(80),
		)
	}
	narrowHeader := current.headerView(32)
	if got := lipgloss.Width(narrowHeader); got > 32 {
		t.Errorf("header width = %d, want at most 32", got)
	}
	if !strings.Contains(narrowHeader, "…") ||
		!strings.Contains(narrowHeader, "READY") {
		t.Errorf(
			"narrow header = %q, want truncated path and visible state",
			narrowHeader,
		)
	}
	if got := current.viewport.Height(); got != originalViewportHeight {
		t.Errorf(
			"viewport height = %d, want unchanged height %d",
			got,
			originalViewportHeight,
		)
	}
}

func TestShellWorkingDirectoryUsesHomeShortcutAndRemovesControls(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() error = %v", err)
	}
	path := filepath.Join(home, "code", "aice-cli") + "\n\x1b"

	got := shellWorkingDirectory(path)

	wantPrefix := filepath.Join("~", "code", "aice-cli")
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("shell working directory = %q, want prefix %q", got, wantPrefix)
	}
	if strings.ContainsAny(got, "\n\x1b") {
		t.Errorf("shell working directory contains terminal controls: %q", got)
	}
}

func TestModelStatusLineShowsModelAndReasoningInsteadOfScrollPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		thinking     llm.ThinkingLevel
		wantThinking string
	}{
		{
			name:         "provider default reasoning",
			wantThinking: "reasoning default",
		},
		{
			name:         "explicit high reasoning",
			thinking:     llm.ThinkingLevelHigh,
			wantThinking: "reasoning high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newScrollableModel(t)
			current.currentModel = llm.Model{ID: "deepseek-v4-flash"}
			current.thinking = tt.thinking
			originalFooterHeight := lipgloss.Height(current.footerView(80))

			status := current.statusLine(80)
			for _, want := range []string{"deepseek-v4-flash", tt.wantThinking} {
				if !strings.Contains(status, want) {
					t.Errorf("status line = %q, want %q", status, want)
				}
			}
			if strings.Contains(status, "%") {
				t.Errorf("status line still contains scroll percentage: %q", status)
			}
			if got := lipgloss.Height(current.footerView(80)); got != originalFooterHeight {
				t.Errorf(
					"footer height = %d, want unchanged height %d; "+
						"status width = %d, footer width = %d:\n%s",
					got,
					originalFooterHeight,
					lipgloss.Width(status),
					lipgloss.Width(current.footerView(80)),
					current.footerView(80),
				)
			}
		})
	}
}

func TestModelStatusLineShowsSessionUsageAndEstimatedCost(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.currentModel = llm.Model{ID: "deepseek-v4-flash"}
	current.sessionUsage = llm.Usage{
		InputTokens:      1_200,
		OutputTokens:     456,
		CacheReadTokens:  100,
		CacheWriteTokens: 20,
		Cost:             &llm.Cost{Total: 0.0074},
	}

	wide := current.statusLine(120)
	for _, want := range []string{
		"↑1.2k",
		"↓456",
		"R100",
		"W20",
		"$0.007",
		"deepseek-v4-flash",
	} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide status line = %q, want %q", wide, want)
		}
	}

	standard := current.statusLine(80)
	for _, want := range []string{
		"↑1.2k",
		"↓456",
		"R100",
		"W20",
		"$0.007",
		"deepseek-v4-flash",
	} {
		if !strings.Contains(standard, want) {
			t.Errorf("standard status line = %q, want %q", standard, want)
		}
	}
	if lipgloss.Width(standard) > 80 {
		t.Errorf("standard status width = %d, want at most 80", lipgloss.Width(standard))
	}

	narrow := current.statusLine(60)
	for _, want := range []string{
		"↑1.3k",
		"↓456",
		"$0.007",
		"deepseek-v4-flash",
	} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow status line = %q, want %q", narrow, want)
		}
	}
	if strings.Contains(narrow, "R100") || strings.Contains(narrow, "W20") {
		t.Errorf("narrow status line did not collapse cache detail: %q", narrow)
	}
	if lipgloss.Width(narrow) > 60 {
		t.Errorf("narrow status width = %d, want at most 60", lipgloss.Width(narrow))
	}
}

func TestModelStatusLineShowsZeroUsageBeforeConversation(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.currentModel = llm.Model{ID: "deepseek-v4-flash"}

	standard := current.statusLine(80)
	for _, want := range []string{
		"↑0",
		"↓0",
		"R0",
		"W0",
		"$0.000",
		"deepseek-v4-flash",
	} {
		if !strings.Contains(standard, want) {
			t.Errorf("zero status line = %q, want %q", standard, want)
		}
	}

	narrow := current.statusLine(32)
	for _, want := range []string{"↑0", "↓0", "$0.000"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow zero status line = %q, want %q", narrow, want)
		}
	}
	if strings.Contains(narrow, "R0") || strings.Contains(narrow, "W0") {
		t.Errorf("narrow zero status did not collapse cache detail: %q", narrow)
	}
	if lipgloss.Width(narrow) > 32 {
		t.Errorf("narrow zero status width = %d, want at most 32", lipgloss.Width(narrow))
	}
}

func TestModelAnimatesCompletedAssistantUsage(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	assistant := llm.NewAssistantMessage(llm.Model{
		ID:       "test",
		API:      "test",
		Provider: "test",
	})
	assistant.Usage = llm.Usage{
		InputTokens:  1_200,
		OutputTokens: 456,
		TotalTokens:  1_656,
		Cost:         &llm.Cost{Total: 0.0074},
	}

	changed, command := current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: assistant,
	})
	if !changed || command == nil {
		t.Fatal("assistant completion did not start usage animation")
	}
	if status := current.usageStatus(true); !strings.Contains(status, "↑0") ||
		!strings.Contains(status, "↓0") ||
		!strings.Contains(status, "$0.000") {
		t.Fatalf("usage animation did not start at zero: %q", status)
	}

	generation := current.usageAnimation.generation
	for range usageAnimationFrames {
		current = updateModel(
			t,
			current,
			usageAnimationTickMsg{generation: generation},
		)
	}

	status := current.usageStatus(true)
	for _, want := range []string{"↑1.2k", "↓456", "$0.007"} {
		if !strings.Contains(status, want) {
			t.Errorf("completed usage animation = %q, want %q", status, want)
		}
	}
}

func TestModelRendersRunActivityInTranscriptInsteadOfFooter(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.entries = []transcriptEntry{{kind: entryUser, text: "inspect"}}
	current.running = true
	current.status = "Starting response..."

	assertActivityInTranscript(t, current, "Starting response...")
	if footer := current.footerView(80); strings.Contains(
		footer,
		current.spinner.View(),
	) || strings.Contains(footer, current.status) {
		t.Fatalf("run activity remains in footer: %q", footer)
	}

	assistant := llm.NewAssistantMessage(llm.Model{
		ID:       "test",
		API:      "test",
		Provider: "test",
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageStart,
		Message: assistant,
	})
	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeThinkingDelta,
			Delta: "checking context",
		},
	})
	assertActivityInTranscript(t, current, "Thinking...")

	current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{
			Type:  llm.EventTypeTextDelta,
			Delta: "Inspection",
		},
	})
	if transcript := current.transcriptView(); strings.Contains(
		transcript,
		current.spinner.View(),
	) {
		t.Fatalf("spinner remains after model output starts: %q", transcript)
	}

	completed := assistant
	completed.Content = []llm.ContentPart{
		llm.NewTextContent("Inspection complete.").Part(),
	}
	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: completed,
	})
	call := llm.ToolCall{ID: "call-1", Name: "read"}
	current.applyAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &call,
	})
	assertActivityInTranscript(t, current, "read")

	current.applyAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolCall: &call,
	})
	assertActivityInTranscript(t, current, "Thinking...")
}

func TestModelToolCallsShowRelevantInput(t *testing.T) {
	t.Parallel()

	const bashCommand = "GOCACHE=/tmp/aice-go-cache go test ./internal/tui " +
		"-run TestModelToolCallsShowRelevantInput -count=1\n" +
		"printf 'tool display complete'"
	tests := []struct {
		name      string
		call      llm.ToolCall
		want      string
		notWanted []string
	}{
		{
			name: "bash shows the complete command",
			call: llm.ToolCall{
				ID:   "bash-call",
				Name: "bash",
				Arguments: []byte(
					`{"command":"GOCACHE=/tmp/aice-go-cache go test ./internal/tui ` +
						`-run TestModelToolCallsShowRelevantInput -count=1\n` +
						`printf 'tool display complete'"}`,
				),
			},
			want: "$ " + bashCommand,
		},
		{
			name: "read shows only the path",
			call: llm.ToolCall{
				ID:        "read-call",
				Name:      "read",
				Arguments: []byte(`{"path":"internal/tui/model.go","offset":10}`),
			},
			want:      "internal/tui/model.go",
			notWanted: []string{"offset"},
		},
		{
			name: "write shows the path without content",
			call: llm.ToolCall{
				ID:   "write-call",
				Name: "write",
				Arguments: []byte(
					`{"path":"notes/output.txt","content":"DO_NOT_RENDER_WRITE_CONTENT"}`,
				),
			},
			want:      "notes/output.txt",
			notWanted: []string{"DO_NOT_RENDER_WRITE_CONTENT"},
		},
		{
			name: "edit shows the path without replacements",
			call: llm.ToolCall{
				ID:   "edit-call",
				Name: "edit",
				Arguments: []byte(
					`{"path":"internal/tui/model.go","edits":[` +
						`{"oldText":"DO_NOT_RENDER_OLD_TEXT",` +
						`"newText":"DO_NOT_RENDER_NEW_TEXT"}]}`,
				),
			},
			want: "internal/tui/model.go",
			notWanted: []string{
				"DO_NOT_RENDER_OLD_TEXT",
				"DO_NOT_RENDER_NEW_TEXT",
			},
		},
		{
			name: "path cannot inject terminal controls",
			call: llm.ToolCall{
				ID:        "unsafe-path-call",
				Name:      "read",
				Arguments: []byte(`{"path":"internal/\u001b[31mmodel.go"}`),
			},
			want:      "internal/�[31mmodel.go",
			notWanted: []string{"\x1b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := newModel(make(chan runRequest), make(chan struct{}))
			current = updateModel(t, current, tea.WindowSizeMsg{
				Width:  80,
				Height: 24,
			})
			current.running = true
			current.applyAgentEvent(agent.AgentEvent{
				Type:     agent.EventTypeToolExecutionStart,
				ToolCall: &tt.call,
			})

			running := ansi.Strip(current.transcriptView())
			for _, wantLine := range strings.Split(tt.want, "\n") {
				if !strings.Contains(running, wantLine) {
					t.Fatalf(
						"running tool transcript = %q, want line %q",
						running,
						wantLine,
					)
				}
			}
			for _, notWanted := range tt.notWanted {
				if strings.Contains(running, notWanted) {
					t.Errorf(
						"running tool transcript contains %q: %q",
						notWanted,
						running,
					)
				}
			}

			current.applyAgentEvent(agent.AgentEvent{
				Type:     agent.EventTypeToolExecutionEnd,
				ToolCall: &tt.call,
			})
			completed := ansi.Strip(current.transcriptView())
			for _, wantLine := range strings.Split(tt.want, "\n") {
				if !strings.Contains(completed, wantLine) {
					t.Errorf(
						"completed tool transcript = %q, want line %q",
						completed,
						wantLine,
					)
				}
			}
		})
	}
}

func TestModelCollapsedProcessHidesDetailsUntilExpanded(t *testing.T) {
	t.Parallel()

	const command = "go test ./internal/tui -run TestModelToolCallsShowRelevantInput\n" +
		"printf 'still visible after collapse'"
	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	processID := current.beginProcess()
	current.entries = []transcriptEntry{
		{
			kind:      entryAssistant,
			thinking:  "HIDDEN_REASONING",
			complete:  true,
			processID: processID,
		},
		{
			kind:       entryTool,
			processID:  processID,
			toolName:   "bash",
			toolDetail: command,
			toolDone:   true,
		},
		{
			kind:       entryAssistant,
			text:       "Final answer",
			complete:   true,
			processID:  processID,
			conclusion: true,
		},
	}
	current.processGroups[0].collapsed = true

	transcript := ansi.Strip(current.transcriptView())
	for _, want := range []string{
		"▶ PROCESS",
		"Final answer",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("collapsed transcript = %q, want %q", transcript, want)
		}
	}
	for _, hidden := range []string{
		"HIDDEN_REASONING",
		"bash",
		"$ go test ./internal/tui -run TestModelToolCallsShowRelevantInput",
		"printf 'still visible after collapse'",
	} {
		if strings.Contains(transcript, hidden) {
			t.Errorf("collapsed transcript still contains %q: %q", hidden, transcript)
		}
	}

	expanded, commandMessage, handled := current.handleKey(tea.KeyPressMsg(tea.Key{
		Code: 'o',
		Mod:  tea.ModCtrl,
	}))
	if !handled || commandMessage != nil {
		t.Fatal("ctrl+o did not expand the collapsed process")
	}
	expandedTranscript := ansi.Strip(expanded.transcriptView())
	for _, want := range []string{
		"HIDDEN_REASONING",
		"bash",
		"$ go test ./internal/tui -run TestModelToolCallsShowRelevantInput",
		"printf 'still visible after collapse'",
		"Final answer",
	} {
		if !strings.Contains(expandedTranscript, want) {
			t.Errorf("expanded transcript = %q, want %q", expandedTranscript, want)
		}
	}
}

func TestModelSpinnerTickRefreshesTranscriptViewport(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.entries = []transcriptEntry{{kind: entryUser, text: "inspect"}}
	current.running = true
	current.status = "Thinking..."
	current.refreshViewport(true)

	previousFrame := current.spinner.View()
	updated := updateModel(t, current, current.spinner.Tick())
	nextFrame := updated.spinner.View()
	if nextFrame == previousFrame {
		t.Fatalf("spinner frame did not advance: %q", nextFrame)
	}
	if viewportView := updated.viewport.View(); !strings.Contains(
		viewportView,
		nextFrame,
	) {
		t.Fatalf(
			"viewport does not contain advanced spinner frame %q: %q",
			nextFrame,
			viewportView,
		)
	}
}

func TestModelAccumulatesCompletedAssistantUsage(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.sessionUsage = llm.Usage{
		InputTokens:  100,
		OutputTokens: 20,
		TotalTokens:  120,
		Cost:         &llm.Cost{Input: 0.01, Total: 0.01},
	}
	assistant := llm.NewAssistantMessage(llm.Model{
		ID:       "test",
		API:      "test",
		Provider: "test",
	})
	assistant.Usage = llm.Usage{
		InputTokens:     50,
		OutputTokens:    30,
		CacheReadTokens: 40,
		TotalTokens:     120,
		Cost: &llm.Cost{
			Output:    0.02,
			CacheRead: 0.001,
			Total:     0.021,
		},
	}

	current.applyAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: assistant,
	})

	got := current.sessionUsage
	if got.InputTokens != 150 ||
		got.OutputTokens != 50 ||
		got.CacheReadTokens != 40 ||
		got.TotalTokens != 240 ||
		got.Cost == nil ||
		got.Cost.Input != 0.01 ||
		got.Cost.Output != 0.02 ||
		got.Cost.CacheRead != 0.001 ||
		got.Cost.Total != 0.031 {
		t.Fatalf("session usage = %#v, want accumulated assistant usage", got)
	}
}

func TestFormatTokensMatchesPiFooterThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int64
		want  string
	}{
		{name: "plain", count: 999, want: "999"},
		{name: "one decimal thousands", count: 1_250, want: "1.3k"},
		{name: "rounded thousands", count: 12_500, want: "13k"},
		{name: "one decimal millions", count: 1_250_000, want: "1.3M"},
		{name: "rounded millions", count: 12_500_000, want: "13M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatTokens(tt.count); got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

func TestModelSlashCommandMenuCompletesAndRunsApplicationCommand(
	t *testing.T,
) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	current := newModel(
		requests,
		make(chan struct{}),
		SlashCommand{Name: "compact", Description: "Compact Session"},
	)
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("/co")

	menu := current.View().Content
	if !strings.Contains(menu, "SLASH COMMANDS") ||
		!strings.Contains(menu, "/compact") {
		t.Fatalf("slash command menu = %q, want compact suggestion", menu)
	}

	completed, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyTab,
	})
	if !handled || command != nil {
		t.Fatal("tab did not complete the selected slash command")
	}
	if got := completed.input.Value(); got != "/compact" {
		t.Fatalf("completed input = %q, want /compact", got)
	}

	starting, command, handled := completed.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil {
		t.Fatal("enter did not submit the completed slash command")
	}
	rawStartMessage := command()
	startMessage, ok := rawStartMessage.(runStartedMsg)
	if !ok {
		t.Fatalf("start command message = %T, want runStartedMsg", rawStartMessage)
	}
	request := <-requests
	if request.command == nil || *request.command != (SlashCommandRequest{Name: "compact"}) {
		t.Fatalf("run request command = %#v, want compact", request.command)
	}

	starting = updateModel(t, starting, startMessage)
	finished := updateModel(t, starting, runBatchMsg{updates: []runUpdate{
		{cancel: func() {}},
		{
			output: "Compacted Session; retained 1 recent turn.",
			done:   true,
		},
	}})
	if finished.running {
		t.Fatal("slash command remains running after terminal update")
	}
	if transcript := finished.transcriptView(); !strings.Contains(
		transcript,
		"Compacted Session",
	) {
		t.Fatalf("command output missing from transcript: %q", transcript)
	}
}

func TestModelSlashCommandMenuCompletesArgumentCommand(t *testing.T) {
	t.Parallel()

	current := newModel(
		make(chan runRequest),
		make(chan struct{}),
		SlashCommand{
			Name:         "checkout",
			Description:  "Move active leaf",
			ArgumentHint: "<entry|root>",
		},
	)
	current.input.SetValue("/check")

	completed, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyTab,
	})
	if !handled || command != nil {
		t.Fatal("tab did not complete the argument command")
	}
	if got := completed.input.Value(); got != "/checkout " {
		t.Fatalf("completed input = %q, want command and trailing space", got)
	}
	if completed.slashCommandMenuVisible() {
		t.Fatal("slash command menu remains visible while entering arguments")
	}
}

func TestModelSecretSlashCommandHidesAndSubmitsSecret(t *testing.T) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	current := newModel(
		requests,
		make(chan struct{}),
		SlashCommand{
			Name:         "login",
			Description:  "Store a provider API key",
			ArgumentHint: "[provider]",
			SecretPrompt: "DeepSeek API key",
		},
	)
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	current.input.SetValue("/login")

	entering, _, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || entering.secretInput == nil {
		t.Fatal("/login did not enter secret input mode")
	}

	const secret = "secret-value"
	entering.input.SetValue(secret)
	if view := entering.View().Content; strings.Contains(view, secret) {
		t.Fatalf("secret input is visible in TUI: %q", view)
	}
	if view := entering.composerView(80); !strings.Contains(view, "••••") {
		t.Fatalf("secret input is not masked: %q", view)
	}

	starting, command, handled := entering.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil {
		t.Fatal("secret input was not submitted")
	}
	rawStartMessage := command()
	if _, ok := rawStartMessage.(runStartedMsg); !ok {
		t.Fatalf(
			"start command message = %T, want runStartedMsg",
			rawStartMessage,
		)
	}
	request := <-requests
	if request.command == nil ||
		request.command.Name != "login" ||
		request.command.Secret != secret {
		t.Fatalf("login request = %#v, want hidden secret", request.command)
	}
	if starting.secretInput != nil || starting.input.Value() != "" {
		t.Fatal("secret remains in composer after submission")
	}
	if transcript := starting.transcriptView(); strings.Contains(
		transcript,
		secret,
	) {
		t.Fatalf("secret leaked into transcript: %q", transcript)
	}
}

func TestModelSecretSlashCommandCanBeCancelledAndRestarted(t *testing.T) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	current := newModel(
		requests,
		make(chan struct{}),
		SlashCommand{
			Name:         "login",
			Description:  "Store a provider API key",
			SecretPrompt: "DeepSeek API key",
		},
	)
	current.input.SetValue("/login")
	entering, _, _ := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	entering.input.SetValue("discarded-secret")

	cancelled, _, handled := entering.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEscape,
	})
	if !handled ||
		cancelled.secretInput != nil ||
		cancelled.input.Value() != "" {
		t.Fatal("Esc did not cancel and clear secret input")
	}
	if transcript := cancelled.transcriptView(); strings.Contains(
		transcript,
		"discarded-secret",
	) {
		t.Fatalf("cancelled secret leaked into transcript: %q", transcript)
	}
	select {
	case request := <-requests:
		t.Fatalf("cancelled login reached run controller: %#v", request)
	default:
	}

	cancelled.input.SetValue("/login")
	restarted, _, handled := cancelled.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || restarted.secretInput == nil {
		t.Fatal("/login could not be restarted after cancellation")
	}
}

func TestModelSlashCommandMenuNavigatesWithArrowKeys(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.input.SetValue("/")

	selected, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyDown,
	})
	if !handled || command != nil {
		t.Fatal("down did not move slash command selection")
	}
	completed, command, handled := selected.handleKey(tea.KeyPressMsg{
		Code: tea.KeyTab,
	})
	if !handled || command != nil {
		t.Fatal("tab did not complete the moved selection")
	}
	if got := completed.input.Value(); got != "/clear" {
		t.Fatalf("completed input = %q, want second command /clear", got)
	}
}

func TestModelSlashCommandMenuKeepsSuggestionsToOneLine(t *testing.T) {
	t.Parallel()

	current := newModel(
		make(chan runRequest),
		make(chan struct{}),
		SlashCommand{Name: "session", Description: "Show current Session information"},
		SlashCommand{Name: "tree", Description: "Show all Session branches and the active leaf"},
		SlashCommand{
			Name:         "checkout",
			Description:  "Move the active leaf without deleting later branches",
			ArgumentHint: "<entry|root>",
		},
		SlashCommand{Name: "compact", Description: "Compact the active branch"},
	)
	current.input.SetValue("/")

	menu := current.slashCommandMenuView(80)
	wantHeight := maximumCommandRows + 3
	if got := lipgloss.Height(menu); got != wantHeight {
		t.Fatalf(
			"slash command menu height = %d, want %d one-line rows:\n%s",
			got,
			wantHeight,
			menu,
		)
	}
}

func TestModelLocalSlashCommandsDoNotReachAgentRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		command     string
		initial     []transcriptEntry
		wantEntries int
		wantText    string
	}{
		{
			name:        "help",
			command:     "/help",
			wantEntries: 2,
			wantText:    "Available slash commands",
		},
		{
			name:    "clear",
			command: "/clear",
			initial: []transcriptEntry{{
				kind: entryAssistant,
				text: "visible only",
			}},
			wantEntries: 0,
		},
		{
			name:        "unknown",
			command:     "/missing",
			wantEntries: 2,
			wantText:    "Unknown slash command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requests := make(chan runRequest, 1)
			current := newModel(requests, make(chan struct{}))
			current.entries = tt.initial
			current.input.SetValue(tt.command)

			updated, command, handled := current.handleKey(tea.KeyPressMsg{
				Code: tea.KeyEnter,
			})
			if !handled || command != nil {
				t.Fatalf("%s returned handled=%v command=%v", tt.command, handled, command)
			}
			if got := len(updated.entries); got != tt.wantEntries {
				t.Fatalf("%s entries = %d, want %d", tt.command, got, tt.wantEntries)
			}
			if tt.wantText != "" &&
				!strings.Contains(updated.transcriptView(), tt.wantText) {
				t.Errorf(
					"%s transcript = %q, want %q",
					tt.command,
					updated.transcriptView(),
					tt.wantText,
				)
			}
			select {
			case request := <-requests:
				t.Fatalf("%s unexpectedly reached run controller: %#v", tt.command, request)
			default:
			}
		})
	}
}

func TestModelCancellationIsRenderedAsNotice(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.finishRun(context.Canceled)

	transcript := current.transcriptView()
	if !strings.Contains(transcript, "Response cancelled") {
		t.Fatalf("transcript does not contain cancellation notice: %q", transcript)
	}
	if strings.Contains(transcript, "Error") {
		t.Fatalf("cancellation was rendered as an error: %q", transcript)
	}
}

func updateModel(t *testing.T, current model, message tea.Msg) model {
	t.Helper()
	updated, _ := current.Update(message)
	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updated)
	}
	return result
}

func assertActivityInTranscript(t *testing.T, current model, want string) {
	t.Helper()

	transcript := current.transcriptView()
	if !strings.Contains(transcript, current.spinner.View()) ||
		!strings.Contains(transcript, want) {
		t.Fatalf(
			"transcript activity = %q, want spinner and %q",
			transcript,
			want,
		)
	}
}

func assertTranscriptGap(
	t *testing.T,
	transcript string,
	before string,
	after string,
	wantNewlines int,
) {
	t.Helper()

	beforeIndex := strings.Index(transcript, before)
	afterIndex := strings.Index(transcript, after)
	if beforeIndex < 0 || afterIndex < 0 || afterIndex <= beforeIndex {
		t.Fatalf(
			"transcript markers %q -> %q not found in order: %q",
			before,
			after,
			transcript,
		)
	}
	gap := transcript[beforeIndex+len(before) : afterIndex]
	firstNewline := strings.IndexByte(gap, '\n')
	if firstNewline < 0 {
		t.Fatalf(
			"transcript gap %q -> %q contains no newline: %q",
			before,
			after,
			gap,
		)
	}
	got := 0
	for _, character := range gap[firstNewline:] {
		if character != '\n' {
			break
		}
		got++
	}
	if got != wantNewlines {
		t.Errorf(
			"transcript gap %q -> %q starts with %d newlines, want %d: %q",
			before,
			after,
			got,
			wantNewlines,
			gap,
		)
	}
}

func newScrollableModel(t *testing.T) model {
	t.Helper()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.viewport.SetContent(strings.Repeat("line\n", 100))
	current.viewport.SetYOffset(20)
	return current
}
