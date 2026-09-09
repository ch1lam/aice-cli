package tool

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	editDiffBytes      = 64 * 1024
	editDiffLines      = 2000
	editDiffInputLines = 100000
	editDiffCells      = 1000000
)

type diffLine struct {
	kind byte
	text string
}

// Compare the bytes actually read and written, including line terminators.
// Bound both alignment work and retained output independently of mutation size.
func editDiff(before, after string) llm.ToolDiff {
	if before == after {
		return llm.ToolDiff{}
	}
	if strings.Count(before, "\n") >= editDiffInputLines || strings.Count(after, "\n") >= editDiffInputLines {
		return llm.ToolDiff{Truncated: true}
	}
	a, b := diffLines(before), diffLines(after)
	prefix := 0
	for prefix < min(len(a), len(b)) && a[prefix] == b[prefix] {
		prefix++
	}
	endA, endB := len(a), len(b)
	for endA > prefix && endB > prefix && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	var lines []diffLine
	for _, line := range a[max(0, prefix-3):prefix] {
		lines = append(lines, diffLine{' ', line})
	}
	lines = append(lines, alignDiff(a[prefix:endA], b[prefix:endB])...)
	for _, line := range a[endA:min(len(a), endA+3)] {
		lines = append(lines, diffLine{' ', line})
	}
	return renderEditDiff(lines, max(0, prefix-3)+1)
}

func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func alignDiff(a, b []string) []diffLine {
	var result []diffLine
	// Large changed spans use an exact but potentially non-minimal replacement.
	// This avoids quadratic allocation or delaying an already completed write.
	if (len(a) + 1) > editDiffCells/(len(b)+1) {
		for _, line := range a {
			result = append(result, diffLine{'-', line})
		}
		for _, line := range b {
			result = append(result, diffLine{'+', line})
		}
		return result
	}
	stride := len(b) + 1
	lcs := make([]int, (len(a)+1)*stride)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*stride+j] = 1 + lcs[(i+1)*stride+j+1]
			} else {
				lcs[i*stride+j] = max(lcs[(i+1)*stride+j], lcs[i*stride+j+1])
			}
		}
	}
	for i, j := 0, 0; i < len(a) || j < len(b); {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			result = append(result, diffLine{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[(i+1)*stride+j] >= lcs[i*stride+j+1]):
			result = append(result, diffLine{'-', a[i]})
			i++
		default:
			result = append(result, diffLine{'+', b[j]})
			j++
		}
	}
	return result
}

func renderEditDiff(lines []diffLine, first int) llm.ToolDiff {
	var out strings.Builder
	rows := 0
	appendRow := func(text string) bool {
		if rows+strings.Count(text, "\n") > editDiffLines || len(text)+out.Len() > editDiffBytes {
			return false
		}
		out.WriteString(text)
		rows += strings.Count(text, "\n")
		return true
	}
	oldLine, newLine := first, first
	for start := 0; start < len(lines); {
		change := start
		for change < len(lines) && lines[change].kind == ' ' {
			change++
		}
		if change == len(lines) {
			break
		}
		hunkStart := max(start, change-3)
		oldLine += hunkStart - start
		newLine += hunkStart - start
		end, lastChange := change+1, change
		for end < len(lines) {
			if lines[end].kind != ' ' {
				if end-lastChange > 7 {
					break
				}
				lastChange = end
			}
			end++
		}
		end = min(len(lines), lastChange+4)
		oldCount, newCount := 0, 0
		for _, line := range lines[hunkStart:end] {
			if line.kind != '+' {
				oldCount++
			}
			if line.kind != '-' {
				newCount++
			}
		}
		o, n := oldLine, newLine
		if oldCount == 0 {
			o--
		}
		if newCount == 0 {
			n--
		}
		if !appendRow(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", o, oldCount, n, newCount)) {
			return llm.ToolDiff{Text: out.String(), Truncated: true}
		}
		for _, line := range lines[hunkStart:end] {
			text := string(line.kind) + line.text
			if !strings.HasSuffix(line.text, "\n") {
				text += "\n\\ No newline at end of file\n"
			}
			if !appendRow(text) {
				return llm.ToolDiff{Text: out.String(), Truncated: true}
			}
		}
		oldLine += oldCount
		newLine += newCount
		start = end
	}
	return llm.ToolDiff{Text: out.String()}
}
