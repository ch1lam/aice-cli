package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func newQuestionTestPrompt() (interaction.QuestionPrompt, chan interaction.QuestionReply) {
	reply := make(chan interaction.QuestionReply, 1)
	done := make(chan struct{})
	return interaction.QuestionPrompt{
		Request: interaction.QuestionRequest{
			ID: "call-1",
			Questions: []interaction.QuestionItem{
				{
					ID:       "mode",
					Header:   "执行方式",
					Question: "采用哪种行为？",
					Options: []interaction.QuestionOption{
						{ID: "check", Label: "仅检查", Description: "先审阅"},
						{ID: "apply", Label: "直接修改"},
					},
					RecommendedOptionID: "check",
				},
				{
					ID:       "goal",
					Question: "目标是什么？",
				},
			},
		},
		Done:  done,
		Reply: reply,
	}, reply
}

func newQuestionTestModel(t *testing.T, prompt interaction.QuestionPrompt) model {
	t.Helper()
	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 30})
	return updateModel(t, current, questionPromptMsg{prompt: prompt})
}

func pressQuestionKey(t *testing.T, current model, message tea.KeyPressMsg) model {
	t.Helper()
	return updateModel(t, current, message)
}

func typeQuestionText(t *testing.T, current model, text string) model {
	t.Helper()
	for _, character := range text {
		current = pressQuestionKey(t, current, tea.KeyPressMsg{
			Code: character,
			Text: string(character),
		})
	}
	return current
}

func questionScreen(t *testing.T, current model) string {
	t.Helper()
	return ansi.Strip(current.View().Content)
}

func TestQuestionPanelRecommendsWithoutAnswering(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	if current.question == nil {
		t.Fatal("question panel did not open")
	}
	if current.question.focus != 0 {
		t.Fatalf("focus = %d, want recommended row 0", current.question.focus)
	}
	if current.question.settled[0] {
		t.Fatal("recommended option must not count as an answer")
	}
	view := questionScreen(t, current)
	if strings.Contains(view, "等待你的回答") {
		t.Fatalf("panel must not show the waiting header:\n%s", view)
	}
	if strings.Contains(view, "1 / 2") {
		t.Fatalf("panel must not show a question counter:\n%s", view)
	}
	if !strings.Contains(view, "推荐") {
		t.Fatalf("panel must mark the recommended option:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	questionAt := -1
	for i, line := range lines {
		if strings.Contains(line, "采用哪种行为？") {
			questionAt = i
			break
		}
	}
	if questionAt < 0 {
		t.Fatalf("panel must show the question:\n%s", view)
	}
	if questionAt+1 >= len(lines) || strings.Trim(lines[questionAt+1], " │") != "" {
		t.Fatalf("panel must separate the question from options with a blank line:\n%s", view)
	}
}

func TestQuestionPanelSpaceSelectSwitchSubmit(t *testing.T) {
	prompt, replies := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	// Draft preservation: the composer value survives the whole exchange.
	current.question.saved = "wip draft"

	// Enter submits the whole group: with nothing answered it stays open
	// and guides to the first unanswered question.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if current.question == nil {
		t.Fatal("incomplete Enter must not submit")
	}
	if current.question.notice == "" {
		t.Fatal("incomplete Enter must prompt for the missing answers")
	}
	select {
	case <-replies:
		t.Fatal("incomplete Enter must not submit")
	default:
	}

	// Q1: move to row 2 and Space-select it; selection stays on Q1.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: ' ', Text: " "})
	if !current.question.settled[0] || current.question.selected[0] != 1 {
		t.Fatalf("q1 not settled on row 2: %#v", current.question)
	}
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 (Space stays, Right switches)", current.question.index)
	}
	// Right switches to Q2; Left returns with the choice kept.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	if current.question.index != 1 {
		t.Fatalf("index = %d, want 1", current.question.index)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyLeft})
	if current.question.index != 0 || current.question.selected[0] != 1 {
		t.Fatalf("choice lost after switch: %#v", current.question)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	// Q2 is free-text: typing then Enter answers it and submits everything.
	current = typeQuestionText(t, current, "修 flaky")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if current.question != nil {
		t.Fatal("panel must close after submitting all answers")
	}
	select {
	case reply := <-replies:
		if reply.RequestID != "call-1" {
			t.Fatalf("reply request = %q", reply.RequestID)
		}
		first := reply.Answers["mode"]
		if first.Status != interaction.QuestionAnswered ||
			first.SelectedOptionID != "apply" ||
			first.SelectedLabel != "直接修改" {
			t.Fatalf("mode answer = %#v", first)
		}
		if second := reply.Answers["goal"]; second.Text != "修 flaky" {
			t.Fatalf("goal answer = %#v", second)
		}
	default:
		t.Fatal("no reply submitted")
	}
	if got := current.input.Value(); got != "wip draft" {
		t.Fatalf("composer draft = %q, want preserved value", got)
	}
}

func TestQuestionPanelSwitchAndSubmit(t *testing.T) {
	prompt, replies := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	// Blank Enter on the custom row must not answer: it keeps the panel
	// open and prompts for the missing answers instead.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if current.question.settled[0] {
		t.Fatal("blank Enter on the custom row must not answer")
	}
	if current.question.notice == "" {
		t.Fatal("blank Enter must prompt for the missing answers")
	}
	// Left on the first question stays; Right/Left switch explicitly.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyLeft})
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 (Left stays on the first question)", current.question.index)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	if current.question.index != 1 {
		t.Fatalf("index = %d, want 1", current.question.index)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyLeft})
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 after left", current.question.index)
	}
	// Backspace only edits text, never switches questions.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 (Backspace must not switch)", current.question.index)
	}
	// Space selects row 2 and stays; Right + text + Enter submits everything.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: ' ', Text: " "})
	if current.question.selected[0] != 1 || !current.question.settled[0] {
		t.Fatalf("q1 not selected: %#v", current.question)
	}
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 (Space stays, Right switches)", current.question.index)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	current = typeQuestionText(t, current, "goal text")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if current.question != nil {
		t.Fatal("panel must submit once every question is settled")
	}
	select {
	case reply := <-replies:
		if first := reply.Answers["mode"]; first.Status != interaction.QuestionAnswered ||
			first.SelectedOptionID != "apply" {
			t.Fatalf("mode = %#v, want answered check", first)
		}
		if second := reply.Answers["goal"]; second.Text != "goal text" {
			t.Fatalf("goal = %#v", second)
		}
	default:
		t.Fatal("no reply submitted")
	}
}

func TestQuestionPanelDigitSelectsOption(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: '2', Text: "2"})
	if current.question.selected[0] != 1 {
		t.Fatalf("digit did not select row 2: %#v", current.question)
	}
	if current.question.index != 0 {
		t.Fatalf("index = %d, want 0 (digit stays, Right switches)", current.question.index)
	}
	if current.question == nil {
		t.Fatal("digit must not submit a partially answered group")
	}
}

func TestQuestionPanelSpaceTypesInTextMode(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	// Space on an option row selects and stays.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: ' ', Text: " "})
	if current.question.selected[0] != 0 {
		t.Fatalf("space did not select row 1: %#v", current.question)
	}
	// Typing enters text mode automatically: a later Space types a
	// supplement instead of reselecting, so answers containing spaces
	// remain typable.
	current = typeQuestionText(t, current, "a b")
	if got := current.question.texts[0]; got != "a b" {
		t.Fatalf("supplement = %q, want %q", got, "a b")
	}
	if current.question.selected[0] != 0 || !current.question.settled[0] {
		t.Fatalf("selection lost after typing a supplement: %#v", current.question)
	}
}

func TestQuestionPanelDigitCustomStaysOnCurrent(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	// Q1 has two options, so digit 3 lands on the custom row. With empty
	// text it must stay on Q1 and focus the input instead of advancing.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: '3', Text: "3"})
	panel := current.question
	if panel == nil {
		t.Fatal("question panel closed after digit custom, want it kept")
	}
	if panel.index != 0 {
		t.Fatalf("index = %d, want 0 after digit custom with empty text", panel.index)
	}
	if panel.settled[0] {
		t.Fatal("digit custom with empty text must not settle the question")
	}
	if panel.focus != panel.customRow() {
		t.Fatalf("focus = %d, want custom row %d", panel.focus, panel.customRow())
	}
	if !panel.textFocus {
		t.Fatal("digit custom must focus the text input")
	}
	// Typing then Enter settles Q1 as a custom answer and guides to Q2.
	current = typeQuestionText(t, current, "先做最小实现")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !current.question.settled[0] || !current.question.custom[0] {
		t.Fatalf("q1 not settled as custom: %#v", current.question)
	}
	if current.question.index != 1 {
		t.Fatalf("index = %d, want 1 after answering q1", current.question.index)
	}
}

func TestQuestionPanelCustomTextSubmit(t *testing.T) {
	prompt, replies := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	// Q1: move to the custom row, type a custom answer, submit-attempt guides to Q2.
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyDown})
	current = typeQuestionText(t, current, "先做最小实现，不增加新依赖")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !current.question.settled[0] || !current.question.custom[0] {
		t.Fatalf("q1 not settled as custom: %#v", current.question)
	}
	// Q2 is free-text: answer it and submit the whole group.
	current = typeQuestionText(t, current, "修 flaky")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	if current.question != nil {
		t.Fatal("panel must close after submitting all answers")
	}
	select {
	case reply := <-replies:
		if err := interaction.ValidateQuestionReply(prompt.Request, reply); err != nil {
			t.Fatalf("panel reply failed validation: %v", err)
		}
		first := reply.Answers["mode"]
		if first.Status != interaction.QuestionAnswered ||
			first.SelectedOptionID != "" ||
			first.SelectedLabel != "" ||
			first.Text != "先做最小实现，不增加新依赖" {
			t.Fatalf("mode custom answer = %#v", first)
		}
		if second := reply.Answers["goal"]; second.Text != "修 flaky" {
			t.Fatalf("goal answer = %#v", second)
		}
	default:
		t.Fatal("no reply submitted")
	}
}

func TestQuestionPanelBrowseKeepsDrafts(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	current = typeQuestionText(t, current, "half")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEscape})
	if current.question == nil || !current.question.browse {
		t.Fatal("Esc must enter browse mode with the panel kept")
	}
	if got := current.question.texts[0]; got != "half" {
		t.Fatalf("draft lost in browse mode: %q", got)
	}
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEscape})
	if current.question.browse {
		t.Fatal("second Esc must return to the panel")
	}
	if got := current.question.texts[0]; got != "half" {
		t.Fatalf("draft lost after browse: %q", got)
	}
}

func TestQuestionPanelExpiryClears(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	current.input.SetValue("")
	current = updateModel(t, current, questionExpiredMsg{reply: prompt.Reply})
	if current.question != nil {
		t.Fatal("expired panel must close")
	}
}

func TestQuestionPanelYieldsToPermissionPrompt(t *testing.T) {
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	current = typeQuestionText(t, current, "half")
	guardReply := make(chan interaction.GuardReply, 1)
	guardReq := interaction.GuardRequest{
		ID:       "g-1",
		ToolName: "bash",
		Command:  "rm -rf /tmp/x",
		Reason:   "dangerous",
		RuleID:   "permissionGate.dangerous",
		Options:  []interaction.GuardOption{{ID: "allow-once", Label: "Allow once"}},
		Reply:    guardReply,
		Done:     make(chan struct{}),
	}
	current = updateModel(t, current, guardRequestMsg{req: &guardReq})
	if current.inputContext().domain != inputGuard {
		t.Fatalf("domain = %v, want guard while permission prompt is pending", current.inputContext().domain)
	}
	view := questionScreen(t, current)
	if !strings.Contains(view, "Run this command?") || strings.Contains(view, "等待你的回答") {
		t.Fatalf("permission prompt must own the screen:\n%s", view)
	}
	// Resolving the permission prompt returns the panel with drafts intact.
	current.guardPending = nil
	current.resizeLayout()
	current.refreshViewport(true)
	if current.question == nil || current.question.texts[0] != "half" {
		t.Fatalf("question drafts lost after permission prompt: %#v", current.question)
	}
	if !current.questionVisible() {
		t.Fatal("question panel must be visible again after permission prompt")
	}
}
