package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestQuestionCustomAnswerReplacesSelectionAcrossQuestions(t *testing.T) {
	prompt, replies := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: '1', Text: "1"})
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: '3', Text: "3"})
	current = typeQuestionText(t, current, "另一种方式")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	current = typeQuestionText(t, current, "目标")
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case reply := <-replies:
		answer := reply.Answers["mode"]
		if answer.SelectedOptionID != "" || answer.SelectedLabel != "" || answer.Text != "另一种方式" {
			t.Fatalf("custom answer reverted to a selection: %+v", answer)
		}
	default:
		t.Fatal("answers were not submitted")
	}
}

func TestQuestionPreservesComposerAttachmentsAndCaret(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expire=%t", expire), func(t *testing.T) {
			prompt, _ := newQuestionTestPrompt()
			current := attachTestFile(t, completionTestModel(), "my folder/main.go")
			current.insertPastePlaceholder(strings.Repeat("draft line\n", 10))
			current.input.MoveToBegin()
			wantText, wantExpanded := current.input.Value(), current.expandComposerText()
			wantFiles := current.composerFiles()
			current = updateModel(t, current, questionPromptMsg{prompt: prompt})
			if strings.Contains(questionScreen(t, current), "my folder") {
				t.Fatal("suspended draft should be hidden")
			}
			if expire {
				current = updateModel(t, current, questionExpiredMsg{reply: prompt.Reply})
			} else {
				current = typeQuestionText(t, current, "answer")
				current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
				current = typeQuestionText(t, current, "goal")
				current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if current.input.Value() != wantText || current.expandComposerText() != wantExpanded ||
				!reflect.DeepEqual(current.composerFiles(), wantFiles) || current.input.cursorOffset() != 0 {
				t.Fatalf("composer changed: text=%q files=%v caret=%d", current.input.Value(), current.composerFiles(), current.input.cursorOffset())
			}
		})
	}
}

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
	return newQuestionTestModelSized(t, prompt, 100, 30)
}

func newQuestionTestModelSized(t *testing.T, prompt interaction.QuestionPrompt, width, height int) model {
	t.Helper()
	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: width, Height: height})
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

// questionFrameRows locates the merged frame's painted rows in a stripped
// screen: the dialog top border, the shared attachment edge, the composer's
// draft row, and the composer's bottom border. While the dialog is attached
// the composer keeps no separate top border of its own.
func questionFrameRows(t *testing.T, view string) (top, attach, draft, bottom int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	draft = -1
	for index, line := range lines {
		// The placeholder is truncated on narrow terminals; match its head.
		if strings.Contains(line, "Ask about") {
			draft = index
			break
		}
	}
	if draft < 1 || draft+1 >= len(lines) {
		t.Fatalf("composer draft row missing:\n%s", view)
	}
	attach = draft - 1
	if strings.Trim(lines[attach], " ") == "" {
		t.Fatalf("blank row separates the dialog from the composer:\n%s", view)
	}
	bottom = draft + 1
	if !strings.Contains(lines[bottom], "╰") {
		t.Fatalf("composer bottom border missing:\n%s", view)
	}
	for index := attach - 1; index >= 0; index-- {
		if strings.HasPrefix(strings.TrimLeft(lines[index], " "), "╭") {
			return index, attach, draft, bottom
		}
	}
	t.Fatalf("dialog top border missing:\n%s", view)
	return 0, 0, 0, 0
}

// cellIndexOfRune reports the terminal cell offset of the nth occurrence
// of a target rune, so painted borders can be compared across CJK text.
func cellIndexOfRune(line string, target rune, occurrence int) (int, bool) {
	width := 0
	found := 0
	for _, r := range line {
		if r == target {
			if found == occurrence {
				return width, true
			}
			found++
		}
		width += lipgloss.Width(string(r))
	}
	return 0, false
}

// The dialog merges with the composer into one frame: both share a single
// edge instead of stacking two borders, the composer keeps its draft row, and
// the dialog height still follows the current option list.
func TestQuestionDialogMergesWithComposer(t *testing.T) {
	for _, width := range []int{100, 60, 32} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			prompt, _ := newQuestionTestPrompt()
			current := newQuestionTestModelSized(t, prompt, width, 30)
			assertQuestionMergedFrame(t, current)
		})
	}
	// Switching to the free-text question drops the option rows and the
	// dialog with them; the shared edge and the composer stay put.
	prompt, _ := newQuestionTestPrompt()
	current := newQuestionTestModel(t, prompt)
	options := current.chrome.question
	current = pressQuestionKey(t, current, tea.KeyPressMsg{Code: tea.KeyRight})
	if current.chrome.question >= options {
		t.Fatalf("dialog height %d did not shrink from %d without options", current.chrome.question, options)
	}
	view := questionScreen(t, current)
	if _, attach, draft, _ := questionFrameRows(t, view); !strings.Contains(strings.Split(view, "\n")[draft], "Ask about this workspace") || !strings.ContainsAny(strings.Split(view, "\n")[attach], "╯╰│") {
		t.Fatalf("merged frame lost after switching questions:\n%s", view)
	}
}

func assertQuestionMergedFrame(t *testing.T, current model) {
	t.Helper()
	view := questionScreen(t, current)
	lines := strings.Split(view, "\n")
	top, attach, _, _ := questionFrameRows(t, view)
	if painted := attach - top + 1; painted != current.chrome.question {
		t.Fatalf("painted dialog rows = %d, measured %d", painted, current.chrome.question)
	}
	if len(lines) != current.height {
		t.Fatalf("painted canvas rows = %d, want terminal height %d", len(lines), current.height)
	}
	pad := current.horizontalPadding()
	indent := min(questionDialogIndent, max((current.layoutWidth()-questionDialogMinimumWidth)/2, 0))
	dialogWidth := max(current.layoutWidth()-2*indent, 1)
	if indent > 0 && dialogWidth >= current.layoutWidth() {
		t.Fatalf("dialog width %d must stay narrower than composer %d", dialogWidth, current.layoutWidth())
	}
	left, right := pad+indent, pad+indent+dialogWidth-1
	attachLine, topLine := lines[attach], lines[top]
	// The walls turn exactly onto the shared edge: rounded corners where a
	// shelf continues, straight walls where the dialog reaches the edge.
	leftJoint, rightJoint := '╯', '╰'
	rightOccurrence := 0
	if indent == 0 {
		// No shelf continues on either side: both joints are straight walls.
		leftJoint, rightJoint, rightOccurrence = '│', '│', 1
	}
	for _, check := range []struct {
		line       string
		target     rune
		occurrence int
		want       int
	}{
		{attachLine, leftJoint, 0, left},
		{attachLine, rightJoint, rightOccurrence, right},
		{topLine, '╭', 0, left},
	} {
		if got, ok := cellIndexOfRune(check.line, check.target, check.occurrence); !ok || got != check.want {
			t.Fatalf("%q at cell %d, want %d: %q", string(check.target), got, check.want, check.line)
		}
	}
	if indent > 0 && (strings.Count(attachLine, "╯") != 1 || strings.Count(attachLine, "╰") != 1) {
		t.Fatalf("shared edge must turn both walls into corners: %q", attachLine)
	}
	// The shared edge spans the whole composer width with its own corners.
	trimmed := strings.TrimRight(attachLine, " ")
	if want := current.horizontalPadding() + current.layoutWidth(); lipgloss.Width(trimmed) != want {
		t.Fatalf("shared edge width = %d, want %d", lipgloss.Width(trimmed), want)
	}
	if indent > 0 && !strings.HasSuffix(trimmed, "╮") {
		t.Fatalf("shared edge must end at the composer corner: %q", attachLine)
	}
	if layout := current.screenLayout(); layout.composer.y != layout.question.y+layout.question.height {
		t.Fatalf("composer does not sit directly under the dialog: %#v", layout)
	}
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
	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 30})
	current.input.SetValue("wip draft")
	current = updateModel(t, current, questionPromptMsg{prompt: prompt})

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
