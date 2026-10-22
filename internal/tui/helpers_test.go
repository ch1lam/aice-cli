package tui

import "strings"

// transcriptView materializes the whole transcript as one explicit snapshot.
// Live frames use lazy items; tests use this to assert on rendered history.
func (m model) transcriptView() string {
	var result strings.Builder
	for i, item := range m.transcriptItems() {
		if i > 0 {
			result.WriteString(strings.Repeat("\n", item.gap+1))
		}
		result.WriteString(item.content().view)
	}
	return result.String()
}
