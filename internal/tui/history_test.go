package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAppendPromptHistoryDeduplicatesConsecutiveEntries(t *testing.T) {
	t.Parallel()

	history := appendPromptHistory(nil, "first")
	history = appendPromptHistory(history, "first")
	history = appendPromptHistory(history, "second")
	history = appendPromptHistory(history, "first")

	if got := strings.Join(history, "|"); got != "first|second|first" {
		t.Errorf("history = %q, want first|second|first", got)
	}
}

func TestAppendPromptHistoryIgnoresBlankAndCapsLength(t *testing.T) {
	t.Parallel()

	history := appendPromptHistory(nil, "")
	if len(history) != 0 {
		t.Fatalf("blank prompt recorded: %#v", history)
	}
	for index := range maximumPromptHistory + 1 {
		history = appendPromptHistory(history, "prompt-"+string(rune('a'+index%26))+string(rune('0'+index%10)))
	}
	if len(history) != maximumPromptHistory {
		t.Fatalf("history length = %d, want %d", len(history), maximumPromptHistory)
	}
	if history[0] == "prompt-a0" {
		t.Errorf("oldest prompt was not dropped: %#v", history[:2])
	}
	if history[maximumPromptHistory-1] == "" {
		t.Errorf("newest prompt was not retained: %#v", history[:2])
	}
}

func TestModelUpDownRecallsSubmittedPrompts(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "first question")
	current = submitPrompt(t, current, "second question")

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled {
		t.Fatal("up did not recall history")
	}
	if got := up.input.Value(); got != "second question" {
		t.Errorf("first up value = %q, want second question", got)
	}
	up, _, handled = up.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled {
		t.Fatal("second up did not recall history")
	}
	if got := up.input.Value(); got != "first question" {
		t.Errorf("second up value = %q, want first question", got)
	}
	down, _, handled := up.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move forward through history")
	}
	if got := down.input.Value(); got != "second question" {
		t.Errorf("down value = %q, want second question", got)
	}
	down, _, handled = down.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down past newest did not restore draft")
	}
	if got := down.input.Value(); got != "" {
		t.Errorf("draft value = %q, want empty", got)
	}
}

func TestModelUpDownRecallsSlashCommands(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "/help")

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled {
		t.Fatal("up did not recall history")
	}
	if got := up.input.Value(); got != "/help" {
		t.Errorf("recalled value = %q, want /help", got)
	}
}

func TestModelRecallPreservesDraft(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "remembered question")
	current.input.SetValue("my in-progress draft")

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled || up.input.Value() != "remembered question" {
		t.Fatalf("up value = %q, handled = %v", up.input.Value(), handled)
	}
	down, _, handled := up.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not return to the draft")
	}
	if got := down.input.Value(); got != "my in-progress draft" {
		t.Errorf("restored draft = %q, want my in-progress draft", got)
	}
}

func TestModelRecallStaysAtOldestAndDoesNotStepBackFromDraft(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "only question")

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled {
		t.Fatal("up did not recall history")
	}
	if got := up.input.Value(); got != "only question" {
		t.Fatalf("recalled value = %q", got)
	}
	up, _, handled = up.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled || up.input.Value() != "only question" {
		t.Errorf("up past oldest changed value = %q, handled = %v", up.input.Value(), handled)
	}
	down, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled || down.input.Value() != "" {
		t.Errorf("down at draft changed value = %q, handled = %v", down.input.Value(), handled)
	}
}

func TestModelUpDownDoesNotRecallWhileRunning(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.promptHistory = []string{"recorded"}
	current.running = true
	current.input.Blur()

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if handled {
		t.Fatal("up was consumed while running")
	}
	if up.input.Value() != "" {
		t.Errorf("running up changed input to %q", up.input.Value())
	}
}

func TestModelUpDownDoesNotRecallSecretInput(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.promptHistory = []string{"recorded"}
	current.secretInput = &secretInput{prompt: "API key"}
	current.input.SetValue("typed")

	up, _, _ := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if up.input.Value() != "typed" {
		t.Errorf("secret input up changed value to %q", up.input.Value())
	}
	if up.historyIndex != -1 {
		t.Errorf("secret input up moved history index to %d", up.historyIndex)
	}
}

func TestModelUpDownDoesNotRecallWhileSelectionMenuOpen(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.promptHistory = []string{"recorded"}
	current.commandMenu = &commandMenuState{
		raw: "/login",
		frames: []commandMenuFrame{{
			menu: SlashCommandMenu{
				Title: "Select provider",
				Options: []SlashCommandOption{{
					Label:     "DeepSeek",
					Arguments: "deepseek",
				}},
			},
		}},
	}
	current.input.SetValue("/login")

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled {
		t.Fatal("up did not stay in the selection menu")
	}
	if got := up.input.Value(); got != "/login" {
		t.Errorf("selection menu up changed value to %q", got)
	}
}

func TestModelMultilineDraftNeverSwitchesHistory(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"older", "newer"}
	current.input.SetValue("line one\nline two\nline three")
	current.input.MoveToBegin()

	if got := current.input.Line(); got != 0 {
		t.Fatalf("initial cursor line = %d, want 0", got)
	}

	up := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if up.input.Value() != "line one\nline two\nline three" {
		t.Fatalf("up at the top of a multi-line draft recalled history: %q", up.input.Value())
	}
	if got := up.input.Line(); got != 0 {
		t.Fatalf("up at the top of a multi-line draft cursor line = %d, want 0", got)
	}
	if up.historyIndex != -1 {
		t.Fatalf("up at the top of a multi-line draft moved history index to %d", up.historyIndex)
	}

	down := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	if down.input.Value() != "line one\nline two\nline three" {
		t.Fatalf("down changed the draft value to %q", down.input.Value())
	}
	if got := down.input.Line(); got != 1 {
		t.Fatalf("down cursor line = %d, want 1", got)
	}
	if down.historyIndex != -1 {
		t.Fatalf("down moved history index to %d", down.historyIndex)
	}
}

func TestModelMultilineDraftDownAtBottomDoesNotSwitch(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"older", "newer"}
	current.input.SetValue("line one\nline two\nline three")
	current.input.CursorEnd()

	if got := current.input.Line(); got != 2 {
		t.Fatalf("end cursor line = %d, want 2", got)
	}

	down := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	if down.input.Value() != "line one\nline two\nline three" {
		t.Fatalf("down at the bottom of a multi-line draft recalled history: %q", down.input.Value())
	}
	if got := down.input.Line(); got != 2 {
		t.Fatalf("down at the bottom cursor line = %d, want 2", got)
	}
	if down.historyIndex != -1 {
		t.Fatalf("down at the bottom moved history index to %d", down.historyIndex)
	}
}

func TestModelRecalledMultilineEntryKeepsSwitching(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"older", "multi\nline\nentry", "newer"}

	up := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := up.input.Value(); got != "newer" {
		t.Fatalf("first up value = %q, want newer", got)
	}
	up = updateModel(t, up, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := up.input.Value(); got != "multi\nline\nentry" {
		t.Fatalf("second up value = %q, want multi-line entry", got)
	}
	if !up.historyBackAllowed() || !up.historyForwardAllowed() {
		t.Fatal("multi-line recalled entry is not allowed to keep switching")
	}
	up = updateModel(t, up, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := up.input.Value(); got != "older" {
		t.Fatalf("third up value = %q, want older", got)
	}

	down := updateModel(t, up, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := down.input.Value(); got != "multi\nline\nentry" {
		t.Fatalf("down value = %q, want multi-line entry", got)
	}
	down = updateModel(t, down, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := down.input.Value(); got != "newer" {
		t.Fatalf("second down value = %q, want newer", got)
	}
	down = updateModel(t, down, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := down.input.Value(); got != "" {
		t.Fatalf("down past newest restored %q, want empty draft", got)
	}
}

func TestModelEditingRecalledPromptStopsHistorySwitching(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"multi\nline\ncommand", "newer"}

	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := current.input.Value(); got != "multi\nline\ncommand" {
		t.Fatalf("recalled value = %q, want multi-line command", got)
	}
	if current.historyIndex != 0 {
		t.Fatalf("history index = %d, want 0 while recalling", current.historyIndex)
	}

	current = updateModel(t, current, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := current.input.Value(); got != "multi\nline\ncommandx" {
		t.Fatalf("edited value = %q, want appended x", got)
	}
	if current.historyIndex != -1 {
		t.Fatalf("editing recalled text kept history index at %d", current.historyIndex)
	}

	up := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := up.input.Value(); got != "multi\nline\ncommandx" {
		t.Fatalf("up after edit switched history to %q", got)
	}
	if up.historyIndex != -1 {
		t.Fatalf("up after edit moved history index to %d", up.historyIndex)
	}
}

func TestModelEditingRecalledSingleLineKeepsEditAsDraft(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"cmd", "newer"}

	current = updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := current.input.Value(); got != "newer" {
		t.Fatalf("recalled value = %q, want newer", got)
	}
	current = updateModel(t, current, tea.KeyPressMsg{Code: '2', Text: "2"})
	if current.historyIndex != -1 {
		t.Fatalf("editing kept history index at %d", current.historyIndex)
	}

	up := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := up.input.Value(); got != "newer" {
		t.Fatalf("up after edit = %q, want recalled newest", got)
	}
	down := updateModel(t, up, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := down.input.Value(); got != "newer2" {
		t.Fatalf("down did not restore edited draft: %q", got)
	}
}

func TestModelSubmitResetsRecallPosition(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "first")
	current.promptHistory = append(current.promptHistory, "older")
	current.historyIndex = 1
	current.historyDraft = "stale draft"
	current.input.SetValue("newest")

	current = submitPrompt(t, current, "newest")
	if got := strings.Join(current.promptHistory, "|"); got != "first|older|newest" {
		t.Errorf("history after submit = %q, want first|older|newest", got)
	}
	if current.historyIndex != -1 || current.historyDraft != "" {
		t.Errorf(
			"recall position not reset: index = %d, draft = %q",
			current.historyIndex,
			current.historyDraft,
		)
	}

	up, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !handled || up.input.Value() != "newest" {
		t.Errorf("up after submit value = %q, handled = %v, want newest", up.input.Value(), handled)
	}
}

func TestModelBTWDoesNotEnterMainPromptHistory(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	current.promptHistory = []string{"main prompt"}
	updated, message := submitSide(t, current, "/btw side question")
	if _, ok := message.(sideRunStartedMsg); !ok {
		t.Fatalf("BTW message = %#v", message)
	}
	if got := strings.Join(updated.promptHistory, "|"); got != "main prompt" {
		t.Fatalf("main prompt history = %q", got)
	}
}

func TestModelHistoryIsNotSecretInTranscript(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = submitPrompt(t, current, "visible question")
	current.historyDraft = "not a transcript entry"

	if transcript := current.transcriptView(); strings.Contains(transcript, "not a transcript entry") {
		t.Errorf("history draft leaked into transcript: %q", transcript)
	}
}

func submitPrompt(t *testing.T, current model, prompt string) model {
	t.Helper()
	current.input.SetValue(prompt)
	updated, _, handled := current.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled {
		t.Fatalf("enter did not submit %q", prompt)
	}
	if updated.running {
		updated.finishRun(nil)
	}
	if updated.running || !updated.input.Focused() {
		t.Fatalf("submit of %q did not leave an idle focused composer", prompt)
	}
	updated.currentModel = DisplayModel{ID: "test"}
	return updated
}
