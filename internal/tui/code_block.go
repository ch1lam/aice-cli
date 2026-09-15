package tui

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// codeBlock owns literal source, independently of display escaping and wrapping.
// Source includes its original line terminators; a final newline is not another
// source line. Callers own byte/line limits and whether the source is incomplete.
type codeBlock struct {
	source   string
	language string
	lines    []string
}

func newCodeBlock(source, language string) codeBlock {
	var lines []string
	if source != "" {
		lines = strings.SplitAfter(source, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	return codeBlock{source: source, language: language, lines: lines}
}

type codeBlockOptions struct {
	width      int
	emptyText  string
	incomplete bool
	// Previews clip before highlighting to bound work on oversized source rows.
	clip bool
}

type codeBlockRow struct {
	text string
	// Zero-based original line; -1 means panel padding, never copyable source.
	sourceLine int
}

type codeBlockLayout struct {
	block                codeBlock
	rows                 []codeBlockRow
	width, contentColumn int
	copyColumn           int
}

func (b codeBlock) layout(options codeBlockOptions) codeBlockLayout {
	width := max(options.width, 6)
	inner := width - 4
	gutter := len(strconv.Itoa(max(len(b.lines), 1))) + 2
	if inner-gutter < 8 {
		gutter = 0
	}
	inner -= gutter
	display := make([]string, len(b.lines))
	for i, line := range b.lines {
		display[i] = escapeCodeRow(strings.TrimSuffix(line, "\n"))
		if options.clip {
			display[i] = ansi.Truncate(display[i], inner, "…")
		}
	}
	highlighted := highlightCodeRows(display, b.language)
	var body []string
	indices := []int{-1}
	for i, line := range highlighted {
		for j, part := range wrapCodeRow(line, inner) {
			prefix := strings.Repeat(" ", gutter)
			if gutter > 0 && j == 0 {
				number := strconv.Itoa(i + 1)
				prefix = mutedStyle.Render(strings.Repeat(" ", gutter-2-len(number)) + number + "  ")
			}
			body = append(body, prefix+part)
			indices = append(indices, i)
		}
	}
	if len(body) == 0 {
		for _, part := range wrapCodeRow(escapeCodeRow(options.emptyText), width-4) {
			body = append(body, part)
			indices = append(indices, -1)
		}
	}
	indices = append(indices, -1)
	panel := strings.Split(blockPanel(strings.Join(body, "\n"), width), "\n")
	result := codeBlockLayout{block: b, rows: make([]codeBlockRow, len(panel)), width: width, contentColumn: 2 + gutter}
	labelWidth := width - 4
	button := ""
	if b.source != "" && labelWidth >= 10 {
		button = "[Copy]"
		result.copyColumn = width - 2 - len(button)
		labelWidth -= len(button) + 1
	}
	header := b.lineSummary(options.incomplete, labelWidth)
	if button != "" {
		header += strings.Repeat(" ", width-4-ansi.StringWidth(header)-len(button)) + button
	}
	panel[0] = strings.Split(blockPanel(mutedStyle.Render(header), width), "\n")[1]
	for i, row := range panel {
		result.rows[i] = codeBlockRow{text: row, sourceLine: indices[i]}
	}
	return result
}

func (b codeBlock) lineSummary(incomplete bool, width int) string {
	count := strconv.Itoa(len(b.lines))
	label := count + " lines"
	if len(b.lines) == 1 {
		label = count + " line"
	}
	if incomplete {
		label += " · partial"
	}
	language := escapeCodeRow(b.language)
	if language != "" && language != "text" && ansi.StringWidth(label+" · "+language) <= width {
		label += " · " + language
	}
	if ansi.StringWidth(label) > width {
		label = count + "L"
		if incomplete {
			label = count + "+L"
		}
	}
	return ansi.Truncate(label, width, "…")
}

// Input is escaped text plus terminal16m's generated SGR, never raw terminal
// input. Wrap in one pass, preserving graphemes and making every row independent
// of the previous row's color state. Repeatedly cutting a long ANSI line would
// rescan its entire prefix for each visual row.
func wrapCodeRow(line string, width int) []string {
	var rows []string
	var row strings.Builder
	var state byte
	cells := 0
	style := ""
	for len(line) > 0 {
		seq, size, n, next := ansi.DecodeSequence(line, state, nil)
		line, state = line[n:], next
		if size > 0 && cells+size > width && cells > 0 {
			rows = append(rows, row.String()+"\x1b[0m")
			row.Reset()
			row.WriteString(style)
			cells = 0
		}
		row.WriteString(seq)
		cells += size
		if seq == "\x1b[0m" || seq == "\x1b[m" {
			style = ""
		} else if strings.HasPrefix(seq, "\x1b[") {
			style += seq
		}
	}
	return append(rows, row.String()+"\x1b[0m")
}

// Keep the original terminators when bounding caller-supplied source. A final
// newline terminates a line; it does not itself mean more source was omitted.
func codeLinePrefix(source string, limit int) (string, bool) {
	end := 0
	for range limit {
		next := strings.IndexByte(source[end:], '\n')
		if next < 0 {
			return source, false
		}
		end += next + 1
	}
	return source[:end], end < len(source)
}

func (l codeBlockLayout) view() string {
	rows := make([]string, len(l.rows))
	for i, row := range l.rows {
		rows[i] = row.text
	}
	return strings.Join(rows, "\n")
}

func highlightCodeRows(rows []string, language string) []string {
	if len(rows) == 0 || language == "" || language == "text" {
		return rows
	}
	style := inkMarkdownStyle()
	style.CodeBlock.Margin = uintPointer(0)
	style.CodeBlock.Indent = uintPointer(0)
	context := glamouransi.NewRenderContext(glamouransi.Options{
		Styles: style, ChromaFormatter: "terminal16m",
	})
	element := glamouransi.CodeBlockElement{Code: strings.Join(rows, "\n") + "\n", Language: language}
	var out bytes.Buffer
	if err := element.Render(&out, context); err != nil {
		return rows
	}
	highlighted := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	// Lexers may normalize whitespace. Source fidelity wins over highlighting.
	if len(highlighted) != len(rows) {
		return rows
	}
	for i, row := range highlighted {
		if ansi.Strip(row) != rows[i] {
			return rows
		}
	}
	return highlighted
}

// Preserve code punctuation and indentation while making terminal controls
// visible. Diff rows separately escape backslashes to distinguish CRLF changes.
func escapeCodeRow(row string) string {
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(row, "�") {
		switch {
		case r == '\t':
			out.WriteString("    ")
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			out.WriteString(escapeDiffRow(string(r)))
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func blockPanel(body string, width int) string {
	panel := lipgloss.NewStyle().Foreground(primaryTextColor).
		Padding(1, 2).Width(max(width, 5)).Render(body)
	// Restore the shared panel background after token resets, including blank
	// rows and wrapped-line padding. Diff rows retain their explicit backgrounds.
	background := ansi.Style{}.BackgroundColor(panelBlackColor).String()
	reset := "\x1b[0m"
	restore := strings.NewReplacer(
		"\x1b[m", reset+background,
		reset, reset+background,
		"\x1b[49m", "\x1b[49m"+background,
	)
	rows := strings.Split(panel, "\n")
	for i, row := range rows {
		rows[i] = background + restore.Replace(row) + reset
	}
	return strings.Join(rows, "\n")
}
