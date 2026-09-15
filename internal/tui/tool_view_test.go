package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestToolPathFoldsInHeader(t *testing.T) {
	for _, name := range []string{"read", "ls", "find", "grep", "edit", "write"} {
		for _, target := range []struct{ path, base string }{
			{"/workspace/internal/main.go", "main.go"},
			{"/workspace/internal/", "internal"},
			{`C:\workspace\目录\main.go`, "main.go"},
			{"/workspace/my  files/中文.go", "中文.go"},
			{"/", "/"},
		} {
			t.Run(name+target.path, func(t *testing.T) {
				m := newModel(nil, nil)
				m.width = 100
				entry := transcriptEntry{toolName: name, toolDetail: target.path, toolDone: true}
				collapsed := ansi.Strip(m.toolHeaderView(entry))
				if !strings.HasSuffix(collapsed, "  "+target.base) {
					t.Fatalf("collapsed header = %q", collapsed)
				}
				if ansi.Strip(m.toolHeaderStyled(entry, true)) != collapsed {
					t.Fatal("hover changed collapsed path")
				}
				entry.toolExpanded = true
				if got := ansi.Strip(m.toolHeaderView(entry)); !strings.HasSuffix(got, "  "+target.path) {
					t.Fatalf("expanded header lost full path: %q", got)
				}
				if strings.Contains(ansi.Strip(m.toolBodyView(entry)), target.path) {
					t.Fatal("path duplicated in body")
				}
			})
		}
	}
}

func TestReadOutputCodePanel(t *testing.T) {
	for _, width := range []int{28, 50, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := newModel(nil, nil)
			m.width = width
			entry := transcriptEntry{toolName: "read", toolDetail: "src/main.go", toolDone: true, toolExpanded: true,
				toolOutput: interaction.ToolOutputDisplay{Available: true, Text: "package main\n\nfunc main() {\n\tprintln(\"hello\\n\")\n}\n"}}
			view := m.toolBodyView(entry)
			plain := ansi.Strip(view)
			if strings.ContainsAny(plain, "╭╰│") || !strings.Contains(plain, "package") {
				t.Fatalf("missing code panel: %s", plain)
			}
			for _, row := range strings.Split(view, "\n") {
				if ansi.StringWidth(row) != m.contentWidth()-8 {
					t.Fatalf("panel overflows at width %d: %q", width, row)
				}
			}
			assertToolBackground(t, view)
			if width == 100 {
				if !strings.Contains(plain, `println("hello\n")`) || strings.Contains(plain, `hello\\n`) {
					t.Fatalf("code punctuation changed: %s", plain)
				}
				// Glamour's keyword foreground differs from plain output styling.
				if !strings.Contains(view, "\x1b[38;2;255;107;107m") {
					t.Fatalf("Go keyword not highlighted: %q", view)
				}
			}
		})
	}
}

func TestToolCodeBackgroundCoversMarkdownAndWrappedRows(t *testing.T) {
	source := "# AICE\n\n<p align=\"center\">\n  ![badge](https://example.com/" + strings.Repeat("long/", 30) + ")\n</p>\n\n```sh\nprintf hello\n```"
	view := toolCodeView(source, "md", 48)
	assertToolBackground(t, view)
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) != 48 {
			t.Fatalf("non-rectangular code block: %q", row)
		}
	}
}

// Inspect SGR state at every text span, including spaces. Stripping ANSI alone
// misses transparent holes after syntax highlighting resets its background.
func assertToolBackground(t *testing.T, view string) {
	t.Helper()
	sgr := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	for _, row := range strings.Split(view, "\n") {
		painted, cursor := false, 0
		for _, match := range sgr.FindAllStringSubmatchIndex(row, -1) {
			if match[0] > cursor && !painted {
				t.Fatalf("transparent text or padding: %q", row[cursor:match[0]])
			}
			params := strings.Split(row[match[2]:match[3]], ";")
			for i := 0; i < len(params); i++ {
				code, _ := strconv.Atoi(params[i])
				switch {
				case code == 0 || code == 49:
					painted = false
				case code == 38 || code == 48:
					if code == 48 {
						painted = true
					}
					if i+1 < len(params) && params[i+1] == "5" {
						i += 2
					} else {
						i += 4
					}
				case code >= 40 && code <= 47, code >= 100 && code <= 107:
					painted = true
				}
			}
			cursor = match[1]
		}
		if cursor < len(row) && !painted {
			t.Fatalf("transparent trailing padding: %q", row[cursor:])
		}
	}
}

func TestMutationHeaderStatsAndCompletedWriteView(t *testing.T) {
	m := newModel(nil, nil)
	m.width = 100
	for _, name := range []string{"edit", "write"} {
		entry := transcriptEntry{toolName: name, toolDetail: "src/main.go", toolDone: true,
			toolDiff: interaction.DiffDisplay{Text: "@@ -1 +1 @@\n-old\n+new\n", Added: 99, Removed: 33, StatsKnown: true, Truncated: true}}
		if got := ansi.Strip(m.toolHeaderView(entry)); !strings.Contains(got, "main.go  +99 -33") {
			t.Fatalf("missing full counts: %s", got)
		}
		entry.writePreview = &writePreview{}
		entry.writePreview.setContent(ToolDisplay{Content: "UNCOMMITTED_PREVIEW", HasContent: true})
		entry.toolExpanded = true
		body := ansi.Strip(m.toolBodyView(entry))
		if strings.Contains(body, "UNCOMMITTED_PREVIEW") || !strings.Contains(body, "+new") {
			t.Fatalf("completed write must show recorded diff: %s", body)
		}
		entry.toolError = true
		if got := ansi.Strip(m.toolHeaderView(entry)); strings.Contains(got, "+99") {
			t.Fatal("failed mutation claimed successful counts")
		}
	}
}

func TestToolBodyShowsOutputLimitsAndEscapesControls(t *testing.T) {
	m := newModel(nil, nil)
	m.width = 80
	entry := transcriptEntry{toolDone: true, toolOutput: interaction.ToolOutputDisplay{
		Available: true, Text: "\x1b]52;c;evil\a\u202e\n" + strings.Repeat("row\n", 2100),
	}}
	view := ansi.Strip(m.toolBodyView(entry))
	if strings.ContainsAny(view, "\x1b\a\u202e") || !strings.Contains(view, `\x1b]52`) {
		t.Fatal("output controls were not escaped")
	}
	if strings.Count(view, "row\n") > 1999 || !strings.Contains(view, "output display limit reached") {
		t.Fatal("missing output bounds")
	}
	entry.toolOutput = interaction.ToolOutputDisplay{Available: true}
	if !strings.Contains(m.toolBodyView(entry), "empty output") {
		t.Fatal("empty output not distinguished")
	}
	entry.toolOutput.Available = false
	if !strings.Contains(m.toolBodyView(entry), "output unavailable") {
		t.Fatal("missing output not distinguished")
	}
}
