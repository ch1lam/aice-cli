package tui

import (
	"path"
	"strings"
	"unicode"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func toolCodeLanguage(filename string) string {
	filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
	switch strings.ToLower(filename) {
	case "dockerfile", "makefile":
		return strings.ToLower(filename)
	}
	language := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	if language == "" {
		return "text"
	}
	for _, r := range language {
		if r < 'a' || r > 'z' {
			return "text"
		}
	}
	return language
}

// Preserve code punctuation and indentation while making terminal controls
// visible. Diff rows separately escape backslashes to distinguish CRLF changes.
func escapeToolOutputRow(row string) string {
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

func toolCodeView(source, language string, width int) string {
	inner := max(width-4, 1)
	// Content may itself contain Markdown fences. Keep it literal inside one
	// code block, including files such as README.md and untrusted tool output.
	fence := "```"
	for strings.Contains(source, fence) {
		fence += "`"
	}
	style := inkMarkdownStyle()
	style.CodeBlock.Margin = uintPointer(0)
	style.CodeBlock.Indent = uintPointer(0)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style), glamour.WithWordWrap(inner),
		// Keep token backgrounds identical to the surrounding RGB panel;
		// Bubble Tea handles downsampling for terminals with fewer colors.
		glamour.WithChromaFormatter("terminal16m"),
	)
	result := source
	if err == nil {
		if rendered, renderErr := renderer.Render(fence + language + "\n" + source + "\n" + fence); renderErr == nil {
			// Glamour inserts one entering newline before a fenced block,
			// possibly preceded by SGR codes. The panel supplies its own padding.
			if first, rest, ok := strings.Cut(rendered, "\n"); ok && strings.TrimSpace(ansi.Strip(first)) == "" {
				rendered = rest
			}
			result = strings.TrimRight(rendered, "\r\n")
		}
	}
	return toolPanel(ansi.Hardwrap(result, inner, true), width)
}

func toolPanel(body string, width int) string {
	panel := lipgloss.NewStyle().Foreground(primaryTextColor).
		Padding(1, 2).Width(max(width, 5)).Render(body)
	// Syntax tokens reset SGR state. Restore the panel background after each
	// reset, including on blank rows and the padding after wrapped source lines,
	// just as the assistant's Markdown code blocks do.
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
