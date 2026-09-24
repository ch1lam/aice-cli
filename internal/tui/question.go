package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// questionPanel is the bottom Q&A panel state. It keeps a display copy of one
// live QuestionPrompt: drafts, focus, and per-question answers live here
// until an explicit submit sends exactly one QuestionReply. The main composer
// remains untouched while its draft is hidden.
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
	// Negative scroll follows the focused option or the end of the answer.
	scroll int
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
	if p.hasOptions() && !p.custom[p.index] && !p.skipped[p.index] && p.selected[p.index] >= 0 {
		p.focus = p.selected[p.index]
	}
	if p.custom[p.index] || !p.hasOptions() {
		p.focus = p.customRow()
	}
	p.textFocus = false
	p.notice = ""
	p.scroll = -1
}

func (p *questionPanel) moveFocus(delta int) {
	if !p.hasOptions() {
		return
	}
	p.focus = min(max(p.focus+delta, 0), p.rows()-1)
	p.scroll = -1
}

// typeText appends user input to the current text field. Typing without a
// selection drafts a custom answer (settled by Enter); typing after a
// selection keeps the choice and treats the text as a supplement.
func (p *questionPanel) typeText(value string) {
	if value == "" {
		return
	}
	if utf8.RuneCountInString(p.texts[p.index])+utf8.RuneCountInString(value) > interaction.MaxAnswerTextRunes {
		p.notice = fmt.Sprintf("回答最多 %d 字，请缩短输入", interaction.MaxAnswerTextRunes)
		p.scroll = -1
		return
	}
	p.texts[p.index] += value
	p.skipped[p.index] = false
	p.textFocus = true
	if p.hasOptions() {
		if p.focus == p.customRow() || p.selected[p.index] < 0 {
			p.selected[p.index] = -1
			p.custom[p.index] = true
			p.settled[p.index] = false
			p.focus = p.customRow()
		}
	} else {
		p.custom[p.index] = true
		p.settled[p.index] = false
	}
	p.notice = ""
	p.scroll = -1
}

func (p *questionPanel) backspace() {
	runes := []rune(p.texts[p.index])
	if len(runes) == 0 {
		return
	}
	p.texts[p.index] = string(runes[:len(runes)-1])
	p.notice = ""
	p.scroll = -1
}

// selectFocused records the focused option for the current question and
// stays on it; Enter submits the whole group separately.
func (p *questionPanel) selectFocused() {
	if !p.hasOptions() {
		return
	}
	if p.focus == p.customRow() {
		// The custom row holds no option: focus the text field so the
		// answer (including spaces) stays typable.
		p.textFocus = true
		p.selected[p.index] = -1
		p.custom[p.index] = true
		p.skipped[p.index] = false
		p.settled[p.index] = false
		p.notice = ""
		p.scroll = -1
		return
	}
	p.selected[p.index] = p.focus
	p.custom[p.index] = false
	p.skipped[p.index] = false
	p.settled[p.index] = true
	p.textFocus = false
	p.notice = ""
	p.scroll = -1
}

// shouldSpaceSelect reports whether Space chooses the focused option.
// Otherwise Space stays a text key so supplements, custom answers, and
// free-text answers containing spaces remain typable.
func (p *questionPanel) shouldSpaceSelect() bool {
	return p.hasOptions() && !p.textFocus && p.focus != p.customRow()
}

// submitAttempt finalizes text drafts (custom and free-text answers settle
// on submit) and reports whether every question is answered or skipped.
func (p *questionPanel) submitAttempt() bool {
	for i := range p.prompt.Request.Questions {
		if p.skipped[i] {
			p.settled[i] = true
			continue
		}
		if p.selected[i] >= 0 && p.selected[i] < len(p.prompt.Request.Questions[i].Options) {
			p.custom[i] = false
			p.settled[i] = true
			continue
		}
		p.selected[i] = -1
		if strings.TrimSpace(p.texts[i]) != "" {
			p.custom[i] = true
			p.settled[i] = true
			continue
		}
		p.custom[i] = false
		p.settled[i] = false
	}
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

// firstUnsettled moves to the lowest-index question still awaiting an
// answer or skip, so an incomplete submit guides the user forward.
func (p *questionPanel) firstUnsettled() {
	for offset := 0; offset < len(p.settled); offset++ {
		if !p.settled[offset] {
			p.index = offset
			p.focus = p.initialFocus(offset)
			if p.custom[offset] {
				p.focus = p.customRow()
			}
			p.textFocus = false
			p.notice = ""
			p.scroll = -1
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
	m.input.Blur()
	m.resizeLayout()
	m.refreshViewport(true)
	return waitForQuestionExpiry(prompt)
}

func (m *model) closeQuestion() {
	if m.question == nil {
		return
	}
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
		panel.notice = "答案不完整：请回答每一题"
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

// questionDialogIndent insets the Q&A dialog from both sides so it stays
// narrower than the composer it grows out of.
const questionDialogIndent = 4

// questionDialogMinimumWidth is the narrowest readable dialog; the indent
// shrinks before the dialog does.
const questionDialogMinimumWidth = 32

type questionDialogLayout struct {
	indent, width, inner int
	title                string
	rows                 []string
	offset, height       int
}

// Content and focus share one wrapped layout. Only its visible window enters
// the frame, so valid long questions cannot push the controls off screen.
func (m model) layoutQuestionDialog(width int) questionDialogLayout {
	l := questionDialogLayout{indent: min(questionDialogIndent, max((width-questionDialogMinimumWidth)/2, 0))}
	l.width = max(width-2*l.indent, 1)
	l.inner = max(l.width-questionDialogStyle.GetHorizontalFrameSize(), 1)
	panel := m.question
	item := panel.current()
	appendRows := func(view string) {
		l.rows = append(l.rows, strings.Split(ansi.Hardwrap(view, l.inner, true), "\n")...)
	}
	question := questionLine(item)
	first, _, multiline := strings.Cut(question, "\n")
	l.title = truncateTerminalText(first, max(l.width-6, 1))
	// Keep the full prompt scrollable when it cannot fit on the title edge.
	if multiline || l.title != question {
		appendRows(bodyStyle.Render(question))
	}
	appendRows("")
	focus := 0
	for row, option := range item.Options {
		if row == panel.focus {
			focus = len(l.rows)
		}
		appendRows(m.questionOptionRow(l.inner, panel, row, option))
	}
	if panel.hasOptions() {
		if panel.focus == panel.customRow() {
			focus = len(l.rows)
		}
		appendRows(m.questionCustomRow(panel))
	}
	if !panel.hasOptions() || (!panel.custom[panel.index] && panel.texts[panel.index] != "") {
		appendRows(m.questionTextRow(panel))
	}
	if panel.textFocus || !panel.hasOptions() {
		focus = len(l.rows) - 1
	}
	appendRows("")
	if panel.notice != "" {
		appendRows(noticeStyle.Render(panel.notice))
		focus = len(l.rows) - 1
	}
	// Top border, shared attachment edge and pinned help each consume a row.
	c := m.chrome
	budget := max(m.layoutHeight()-c.header-c.menu-c.composer-c.footer-minimumViewport-3, 1)
	l.height = min(len(l.rows), budget)
	l.offset = panel.scroll
	if l.offset < 0 {
		l.offset = max(focus-l.height+1, 0)
	}
	l.offset = min(max(l.offset, 0), len(l.rows)-l.height)
	return l
}

// questionDialogView paints the bounded body above the shared composer edge.
func (m model) questionDialogView(width int) string {
	if !m.questionVisible() {
		return ""
	}
	l := m.layoutQuestionDialog(width)
	body := strings.Join(l.rows[l.offset:l.offset+l.height], "\n")
	box := questionDialogStyle.Width(l.width).BorderBottom(false).
		Render(body + "\n" + mutedStyle.Render(m.questionHelp(l.inner)))
	// Replace only the top edge; body wrapping and measured height stay shared.
	_, rest, _ := strings.Cut(box, "\n")
	edge := lipgloss.NewStyle().Foreground(secondaryColor)
	heading := " " + l.title + " "
	box = edge.Render("╭─") + bodyStyle.Bold(true).Render(heading) +
		edge.Render(strings.Repeat("─", max(l.width-3-ansi.StringWidth(heading), 0))+"╮") + "\n" + rest
	pad := strings.Repeat(" ", l.indent)
	var painted []string
	for _, line := range strings.Split(box, "\n") {
		painted = append(painted, pad+line)
	}
	painted = append(painted, questionAttachRow(width, l.indent, l.indent+l.width-1))
	return strings.Join(painted, "\n")
}

// questionAttachRow is the single edge shared by the dialog and the composer.
// It keeps the focused yellow tone throughout so the edge reads as
// part of the input frame: corners and shelves run to the dialog walls,
// rounded corners turn the walls into the shelves, and the open neck between
// them reads as the dialog growing out of the input box. A wall that reaches
// the composer edge has no shelf to turn into, so it continues straight down
// onto the composer's own wall instead.
func questionAttachRow(width, left, right int) string {
	edge := lipgloss.NewStyle().Foreground(secondaryColor)
	var row strings.Builder
	if left > 0 {
		row.WriteString(edge.Render("╭" + strings.Repeat("─", left-1) + "╯"))
	} else {
		row.WriteString(edge.Render("│"))
	}
	row.WriteString(strings.Repeat(" ", max(right-left-1, 0)))
	if right < width-1 {
		row.WriteString(edge.Render("╰" + strings.Repeat("─", width-2-right) + "╮"))
	} else {
		row.WriteString(edge.Render("│"))
	}
	return row.String()
}

// questionDialogHeight reports the dialog's painted rows, or 0 when hidden, so
// chrome measurement and painting agree.
func (m model) questionDialogHeight(width int) int {
	if !m.questionVisible() {
		return 0
	}
	return lipgloss.Height(m.questionDialogView(width))
}

// questionLine supplies the border title and, for long prompts, the body.
func questionLine(item *interaction.QuestionItem) string {
	if text := strings.TrimSpace(item.Question); text != "" {
		return sanitizeMultilineText(text)
	}
	return sanitizeSingleLineText(strings.TrimSpace(item.Header))
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
	label := fmt.Sprintf("%s%d %s %s", marker, row+1, selected, sanitizeSingleLineText(option.Label))
	description := sanitizeSingleLineText(option.Description)
	recommend := ""
	recommendStyle := lipgloss.NewStyle().Foreground(secondaryColor)
	if option.ID == panel.current().RecommendedOptionID {
		recommend = "  推荐"
	}
	if description != "" {
		// Wide terminals keep name and description on one row; narrow
		// terminals stack the description below the name.
		if ansi.StringWidth(label+"  "+description+recommend) <= inner {
			return style.Render(label) + mutedStyle.Render("  "+description) + recommendStyle.Render(recommend)
		}
	}
	line := style.Render(label) + recommendStyle.Render(recommend)
	if description != "" {
		indent := strings.Repeat(" ", ansi.StringWidth(marker)+4)
		desc := truncateTerminalText(description, max(inner-ansi.StringWidth(indent), 1))
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
	prefix := style.Render(fmt.Sprintf("%s%d %s ", marker, panel.customRow()+1, selected))
	text := ""
	if panel.custom[panel.index] {
		text = sanitizeMultilineText(panel.texts[panel.index])
	}
	shown := bodyStyle.Render(text)
	if text == "" {
		shown = mutedStyle.Render("或自行撰写回复")
	}
	if panel.textFocus && panel.custom[panel.index] {
		shown += guardCursorStyle.Render(" ")
	}
	return prefix + shown
}

func (m model) questionTextRow(panel *questionPanel) string {
	text := sanitizeMultilineText(panel.texts[panel.index])
	prefix := ""
	if panel.hasOptions() {
		prefix = mutedStyle.Render("补充说明：")
	}
	shown := bodyStyle.Render(text)
	if text == "" {
		shown = mutedStyle.Render("或自行撰写回复")
	}
	if panel.textFocus {
		shown += guardCursorStyle.Render(" ")
	}
	return prefix + shown
}

func (m model) questionHelp(inner int) string {
	help := "Enter 提交 · PgUp/PgDn 滚动 · ↑↓ 选择 · Space 选中 · ←→ 切题"
	help += " · Esc 查看对话 · Ctrl+c 取消运行"
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
	case inputActionQuestionSwitch:
		panel.moveQuestion(match.argument)
		m.resizeLayout()
		m.refreshViewport(false)
	case inputActionQuestionSelect:
		panel.selectFocused()
		m.resizeLayout()
	case inputActionQuestionSubmit:
		if panel.submitAttempt() {
			return m, m.submitQuestion(), true
		}
		panel.firstUnsettled()
		panel.notice = "答案不完整：请回答每一题"
		m.resizeLayout()
		m.refreshViewport(false)
	case inputActionQuestionBackspace:
		panel.backspace()
		m.resizeLayout()
	case inputActionQuestionNewline:
		panel.typeText("\n")
		m.resizeLayout()
	case inputActionQuestionScroll:
		l := m.layoutQuestionDialog(m.layoutWidth())
		panel.scroll = min(max(l.offset+match.argument*l.height, 0), len(l.rows)-l.height)
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

// handleQuestionText routes printable input. Digit answers stay available
// as shortcuts only while the text field is empty and unfocused; any other
// input (or any input into a non-empty field) edits the text. Space never
// reaches here for selection: it is bound to question.select while an option
// row is focused and falls through as text otherwise.
func (m model) handleQuestionText(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.question == nil || m.question.browse || message.Text == "" {
		return m, nil, true
	}
	panel := m.question
	if !panel.textFocus && panel.texts[panel.index] == "" && panel.hasOptions() {
		if len(message.Text) == 1 && message.Text[0] >= '1' && message.Text[0] <= '9' {
			row := int(message.Text[0] - '1')
			if row < panel.rows() {
				panel.focus = row
				// A digit on the custom row with empty text focuses the
				// input for editing; option digits select and stay.
				panel.selectFocused()
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
