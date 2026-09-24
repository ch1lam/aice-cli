package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// questionPanel is the bottom Q&A panel state. It keeps a display copy of one
// live QuestionPrompt: drafts, focus, and per-question answers live here
// until an explicit submit sends exactly one QuestionReply. The main composer
// draft is preserved independently and restored afterwards.
type questionPanel struct {
	prompt interaction.QuestionPrompt
	index  int
	// selected records the chosen option row per question (-1 for none).
	selected []int
	// custom records custom-text answers (or free-text answers).
	custom []bool
	// texts holds per-question text: the answer itself for custom/free-text
	// questions, a supplement otherwise.
	texts []string
	// skipped marks explicit skips; settled marks answered-or-skipped.
	skipped []bool
	settled []bool
	// focus is the highlighted row; textFocus pins typing to the text field
	// so option shortcuts never swallow edited text.
	focus     int
	textFocus bool
	browse    bool
	notice    string
	saved     string
}

func newQuestionPanel(prompt interaction.QuestionPrompt) *questionPanel {
	count := len(prompt.Request.Questions)
	panel := &questionPanel{
		prompt:   prompt,
		selected: make([]int, count),
		custom:   make([]bool, count),
		texts:    make([]string, count),
		skipped:  make([]bool, count),
		settled:  make([]bool, count),
	}
	for i := range panel.selected {
		panel.selected[i] = -1
	}
	panel.focus = panel.initialFocus(0)
	return panel
}

func (p *questionPanel) current() *interaction.QuestionItem {
	return &p.prompt.Request.Questions[p.index]
}

// rows counts the selectable rows of the current question: one per option
// plus a trailing custom-answer row. Free-text questions have no rows.
func (p *questionPanel) rows() int {
	return len(p.current().Options) + 1
}

func (p *questionPanel) hasOptions() bool {
	return len(p.current().Options) > 0
}

func (p *questionPanel) customRow() int {
	return len(p.current().Options)
}

// initialFocus honors the recommended option without answering anything.
func (p *questionPanel) initialFocus(index int) int {
	item := &p.prompt.Request.Questions[index]
	for row, option := range item.Options {
		if option.ID == item.RecommendedOptionID {
			return row
		}
	}
	if len(item.Options) > 0 {
		return 0
	}
	return -1
}

func (p *questionPanel) moveQuestion(delta int) {
	count := len(p.prompt.Request.Questions)
	p.index = min(max(p.index+delta, 0), count-1)
	p.focus = p.initialFocus(p.index)
	// Keep an already recorded choice visible when returning to a question.
	if p.settled[p.index] && p.hasOptions() && !p.custom[p.index] && !p.skipped[p.index] {
		p.focus = p.selected[p.index]
	}
	if p.settled[p.index] && (p.custom[p.index] || !p.hasOptions()) {
		p.focus = p.customRow()
	}
	p.textFocus = false
	p.notice = ""
}

func (p *questionPanel) moveFocus(delta int) {
	if !p.hasOptions() {
		return
	}
	p.focus = min(max(p.focus+delta, 0), p.rows()-1)
}

// typeText appends user input to the current text field. With a selected
// option the text is a supplement; otherwise it becomes the answer itself and
// focus moves to the custom row.
func (p *questionPanel) typeText(value string) {
	if value == "" {
		return
	}
	p.texts[p.index] += value
	p.skipped[p.index] = false
	p.textFocus = true
	if p.hasOptions() && p.selected[p.index] < 0 {
		p.focus = p.customRow()
	}
	p.notice = ""
}

func (p *questionPanel) backspace() {
	runes := []rune(p.texts[p.index])
	if len(runes) == 0 {
		return
	}
	p.texts[p.index] = string(runes[:len(runes)-1])
	p.notice = ""
}

// confirm settles the current question from focus and text. It reports
// whether every question is now settled.
func (p *questionPanel) confirm() bool {
	text := strings.TrimSpace(p.texts[p.index])
	if !p.hasOptions() {
		if text == "" {
			p.notice = "输入答案，或按 Ctrl+s 跳过本题"
			return false
		}
		p.custom[p.index] = true
		p.selected[p.index] = -1
		p.skipped[p.index] = false
		p.settled[p.index] = true
		p.notice = ""
		return p.allSettled()
	}
	if p.focus == p.customRow() {
		if text == "" {
			p.notice = "输入自定义答案，或按 s 跳过本题"
			return false
		}
		p.custom[p.index] = true
		p.selected[p.index] = -1
		p.skipped[p.index] = false
		p.settled[p.index] = true
		p.notice = ""
		return p.allSettled()
	}
	p.selected[p.index] = p.focus
	p.custom[p.index] = false
	p.skipped[p.index] = false
	p.settled[p.index] = true
	p.notice = ""
	return p.allSettled()
}

func (p *questionPanel) skip() bool {
	p.skipped[p.index] = true
	p.settled[p.index] = true
	p.selected[p.index] = -1
	p.textFocus = false
	p.notice = ""
	return p.allSettled()
}

func (p *questionPanel) allSettled() bool {
	for _, settled := range p.settled {
		if !settled {
			return false
		}
	}
	return true
}

// nextUnsettled moves to the first question still awaiting an explicit
// answer or skip.
func (p *questionPanel) nextUnsettled() {
	for offset := 1; offset <= len(p.settled); offset++ {
		next := (p.index + offset) % len(p.settled)
		if !p.settled[next] {
			p.index = next
			p.focus = p.initialFocus(next)
			p.textFocus = false
			p.notice = ""
			return
		}
	}
}

func (p *questionPanel) buildReply() (interaction.QuestionReply, error) {
	request := p.prompt.Request
	reply := interaction.QuestionReply{
		RequestID: request.ID,
		Answers:   make(map[string]interaction.QuestionAnswer, len(request.Questions)),
	}
	for i := range request.Questions {
		item := &request.Questions[i]
		if p.skipped[i] {
			reply.Answers[item.ID] = interaction.QuestionAnswer{Status: interaction.QuestionSkipped}
			continue
		}
		answer := interaction.QuestionAnswer{Status: interaction.QuestionAnswered}
		if p.custom[i] || len(item.Options) == 0 {
			answer.Text = strings.TrimSpace(p.texts[i])
		} else if p.selected[i] < 0 || p.selected[i] >= len(item.Options) {
			// An unsettled option question must fail validation as an
			// incomplete answer, never panic on the option index.
		} else {
			option := &item.Options[p.selected[i]]
			answer.SelectedOptionID = option.ID
			answer.SelectedLabel = option.Label
			answer.Text = strings.TrimSpace(p.texts[i])
		}
		reply.Answers[item.ID] = answer
	}
	if err := interaction.ValidateQuestionReply(request, reply); err != nil {
		return interaction.QuestionReply{}, err
	}
	return reply, nil
}

// questionVisible reports whether the panel replaces the composer.
func (m model) questionVisible() bool {
	return m.question != nil && !m.question.browse && m.guardPending == nil && m.reading == nil
}

type questionPromptMsg struct {
	prompt interaction.QuestionPrompt
}

func waitForQuestionPrompt(requests <-chan interaction.QuestionPrompt) tea.Cmd {
	return func() tea.Msg {
		prompt, ok := <-requests
		if !ok {
			return nil
		}
		return questionPromptMsg{prompt: prompt}
	}
}

func (m model) nextQuestionWait() tea.Cmd {
	if m.questionRequests == nil {
		return nil
	}
	return waitForQuestionPrompt(m.questionRequests)
}

// A cancelled run must not leave an orphaned panel behind.
type questionExpiredMsg struct {
	reply chan interaction.QuestionReply
}

func waitForQuestionExpiry(prompt interaction.QuestionPrompt) tea.Cmd {
	if prompt.Done == nil {
		return nil
	}
	return func() tea.Msg {
		<-prompt.Done
		return questionExpiredMsg{reply: prompt.Reply}
	}
}

func (m *model) openQuestion(prompt interaction.QuestionPrompt) tea.Cmd {
	select {
	case <-prompt.Done:
		return m.nextQuestionWait()
	default:
	}
	// Sequential tool execution allows only one live prompt; a queued
	// successor replaces defensively instead of orphaning its waiter.
	if m.question != nil {
		m.closeQuestion()
	}
	m.question = newQuestionPanel(prompt)
	m.question.saved = m.input.Value()
	m.input.SetValue("")
	m.input.Blur()
	m.status = "等待你的回答…"
	m.resizeLayout()
	m.refreshViewport(true)
	return waitForQuestionExpiry(prompt)
}

func (m *model) closeQuestion() {
	if m.question == nil {
		return
	}
	m.input.SetValue(m.question.saved)
	m.input.CursorEnd()
	m.question = nil
	m.input.Focus()
	m.resizeLayout()
	m.refreshViewport(true)
}

func (m *model) submitQuestion() tea.Cmd {
	panel := m.question
	if panel == nil {
		return m.nextQuestionWait()
	}
	reply, err := panel.buildReply()
	if err != nil {
		panel.notice = "答案不完整：请回答或跳过每一题"
		m.resizeLayout()
		m.refreshViewport(false)
		return nil
	}
	select {
	case panel.prompt.Reply <- reply:
	default:
	}
	m.closeQuestion()
	m.status = "Thinking..."
	return m.nextQuestionWait()
}

func (m model) questionView(width int) string {
	panel := m.question
	if panel == nil {
		return ""
	}
	inner := max(width-4, 20)
	rows := make([]string, 0, 16)
	rows = append(rows, brandStyle.Render("◆ 等待你的回答"))
	item := panel.current()
	title := strings.TrimSpace(item.Header)
	if title == "" {
		title = strings.TrimSpace(item.Question)
	}
	counter := mutedStyle.Render(fmt.Sprintf("%d / %d", panel.index+1, len(panel.prompt.Request.Questions)))
	heading := bodyStyle.Render(title)
	if ansi.StringWidth(title)+ansi.StringWidth(counter)+3 <= inner {
		gap := inner - ansi.StringWidth(title) - ansi.StringWidth(counter)
		heading += strings.Repeat(" ", gap) + counter
	} else {
		heading += "\n" + counter
	}
	rows = append(rows, heading)
	if strings.TrimSpace(item.Header) != "" {
		rows = append(rows, mutedStyle.Render(sanitizeSingleLineText(item.Question)))
	}
	for row, option := range item.Options {
		rows = append(rows, m.questionOptionRow(inner, panel, row, option))
	}
	if panel.hasOptions() {
		rows = append(rows, m.questionCustomRow(panel))
	}
	rows = append(rows, m.questionTextRow(inner, panel))
	if panel.notice != "" {
		rows = append(rows, noticeStyle.Render(panel.notice))
	}
	rows = append(rows, mutedStyle.Render(m.questionHelp(inner)))
	return strings.Join(rows, "\n")
}

func (m model) questionOptionRow(
	inner int,
	panel *questionPanel,
	row int,
	option interaction.QuestionOption,
) string {
	marker := "  "
	style := bodyStyle
	if row == panel.focus && !panel.textFocus {
		marker = "› "
		style = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	}
	selected := "○"
	if panel.settled[panel.index] && panel.selected[panel.index] == row && !panel.custom[panel.index] {
		selected = "●"
	}
	label := fmt.Sprintf("%s%d %s %s", marker, row+1, selected, option.Label)
	recommend := ""
	if option.ID == panel.current().RecommendedOptionID {
		recommend = "  推荐"
	}
	if option.Description != "" {
		// Wide terminals keep name and description on one row; narrow
		// terminals stack the description below the name.
		if ansi.StringWidth(label+"  "+option.Description+recommend) <= inner {
			return style.Render(label) + mutedStyle.Render("  "+option.Description) + mutedStyle.Render(recommend)
		}
	}
	line := style.Render(label) + mutedStyle.Render(recommend)
	if option.Description != "" {
		indent := strings.Repeat(" ", ansi.StringWidth(marker)+4)
		desc := truncateTerminalText(option.Description, max(inner-ansi.StringWidth(indent), 1))
		line += "\n" + indent + mutedStyle.Render(desc)
	}
	return line
}

func (m model) questionCustomRow(panel *questionPanel) string {
	marker := "  "
	style := bodyStyle
	if panel.focus == panel.customRow() {
		marker = "› "
		style = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	}
	selected := "○"
	if panel.settled[panel.index] && panel.custom[panel.index] {
		selected = "●"
	}
	return style.Render(fmt.Sprintf(
		"%s%d %s 自己填写…",
		marker,
		panel.customRow()+1,
		selected,
	))
}

func (m model) questionTextRow(inner int, panel *questionPanel) string {
	text := panel.texts[panel.index]
	caption := "补充说明"
	if !panel.hasOptions() || panel.custom[panel.index] || panel.focus == panel.customRow() {
		caption = "回答"
	}
	shown := text
	if panel.textFocus {
		shown += guardCursorStyle.Render(" ")
	}
	shown = truncateTerminalText(shown, max(inner-ansi.StringWidth(caption)-3, 1))
	return mutedStyle.Render(caption+"：") + bodyStyle.Render(shown)
}

func (m model) questionHelp(inner int) string {
	help := "↑↓ 选择 · Tab 切换输入 · Enter 确认"
	if m.question != nil && m.question.index == len(m.question.prompt.Request.Questions)-1 {
		help = "↑↓ 选择 · Tab 切换输入 · Enter 提交全部答案"
	}
	help += " · s/Ctrl+s 跳过 · Bksp 上一题 · Esc 查看对话 · Ctrl+c 取消运行"
	return truncateTerminalText(help, max(inner, 1))
}

func (m model) handleQuestionAction(match inputActionMatch) (model, tea.Cmd, bool) {
	if m.question == nil {
		return m, nil, false
	}
	panel := m.question
	if panel.browse {
		switch match.action {
		case inputActionQuestionBrowse:
			panel.browse = false
			m.input.Blur()
			m.resizeLayout()
			m.refreshViewport(true)
		case inputActionQuestionScroll:
			if match.argument < 0 {
				m.viewport.PageUp()
			} else {
				m.viewport.PageDown()
			}
		case inputActionQuestionCancel:
			if m.cancelRun != nil {
				m.cancelRun()
			} else {
				m.cancelRequested = true
			}
			m.status = "Cancelling current response..."
		}
		return m, nil, true
	}
	switch match.action {
	case inputActionQuestionBrowse:
		panel.browse = true
		m.resizeLayout()
		m.refreshViewport(true)
	case inputActionQuestionMove:
		panel.textFocus = false
		panel.moveFocus(match.argument)
		m.resizeLayout()
	case inputActionQuestionTab:
		panel.textFocus = !panel.textFocus
		if panel.textFocus && panel.hasOptions() && panel.selected[panel.index] < 0 {
			panel.focus = panel.customRow()
		}
		m.resizeLayout()
	case inputActionQuestionConfirm:
		if panel.confirm() && panel.allSettled() {
			return m, m.submitQuestion(), true
		}
		if panel.settled[panel.index] {
			panel.nextUnsettled()
		}
		m.resizeLayout()
		m.refreshViewport(false)
	case inputActionQuestionSkip:
		if panel.skip() && panel.allSettled() {
			return m, m.submitQuestion(), true
		}
		panel.nextUnsettled()
		m.resizeLayout()
		m.refreshViewport(false)
	case inputActionQuestionBack:
		panel.moveQuestion(-1)
		m.resizeLayout()
		m.refreshViewport(false)
	case inputActionQuestionBackspace:
		// Empty text steps back; otherwise it edits.
		if panel.texts[panel.index] == "" {
			panel.moveQuestion(-1)
		} else {
			panel.backspace()
		}
		m.resizeLayout()
	case inputActionQuestionNewline:
		panel.typeText("\n")
		m.resizeLayout()
	case inputActionQuestionScroll:
		if match.argument < 0 {
			m.viewport.PageUp()
		} else {
			m.viewport.PageDown()
		}
	case inputActionQuestionCancel:
		if m.cancelRun != nil {
			m.cancelRun()
		} else {
			m.cancelRequested = true
		}
		m.status = "Cancelling current response..."
	}
	return m, nil, true
}

// handleQuestionText routes printable input. Single-letter and digit answers
// stay available as shortcuts only while the text field is empty and unfocused;
// any other input (or any input into a non-empty field) edits the text.
func (m model) handleQuestionText(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.question == nil || m.question.browse || message.Text == "" {
		return m, nil, true
	}
	panel := m.question
	if !panel.textFocus && panel.texts[panel.index] == "" && panel.hasOptions() {
		switch message.Text {
		case "s", "S":
			if panel.skip() && panel.allSettled() {
				return m, m.submitQuestion(), true
			}
			panel.nextUnsettled()
			m.resizeLayout()
			m.refreshViewport(false)
			return m, nil, true
		case "b", "B":
			panel.moveQuestion(-1)
			m.resizeLayout()
			m.refreshViewport(false)
			return m, nil, true
		}
		if len(message.Text) == 1 && message.Text[0] >= '1' && message.Text[0] <= '9' {
			row := int(message.Text[0] - '1')
			if row < panel.rows() {
				panel.focus = row
				if panel.confirm() && panel.allSettled() {
					return m, m.submitQuestion(), true
				}
				// Only an explicitly settled question may advance. A digit
				// that lands on the custom row with empty text stays on the
				// current question and focuses the input for editing.
				if panel.settled[panel.index] {
					panel.nextUnsettled()
				} else if row == panel.customRow() {
					panel.textFocus = true
				}
				m.resizeLayout()
				m.refreshViewport(false)
				return m, nil, true
			}
		}
	}
	panel.typeText(message.Text)
	m.resizeLayout()
	return m, nil, true
}

func (m model) handleQuestionPaste(content string) (model, tea.Cmd) {
	if m.question == nil || m.question.browse || content == "" {
		return m, nil
	}
	// Pastes stay literal: no slash commands, file expansion, or steering.
	m.question.typeText(content)
	m.resizeLayout()
	return m, nil
}
