package tui

// historyBackAllowed reports whether Up switches to an earlier prompt instead
// of moving the composer cursor. A single-line composer (including an empty
// one) always switches. Once an entry has been recalled, navigation keeps
// switching even when the recalled entry is multi-line; editing the recalled
// text exits that mode so arrow keys move the cursor for local changes. A
// multi-line draft never switches until the user recalls an entry.
func (m model) historyBackAllowed() bool {
	if m.historyIndex >= 0 {
		return true
	}
	return m.input.LineCount() <= 1 &&
		m.input.Line() == 0 &&
		m.input.LineInfo().RowOffset == 0
}

// historyForwardAllowed reports whether Down moves toward the newest recalled
// prompt. Its single-line rule mirrors historyBackAllowed; pressing Down while
// already on the draft is a harmless no-op handled by recallHistory.
func (m model) historyForwardAllowed() bool {
	if m.historyIndex >= 0 {
		return true
	}
	return m.input.LineCount() <= 1 &&
		m.input.Line() == m.input.LineCount()-1 &&
		m.input.LineInfo().RowOffset+1 == m.input.LineInfo().Height
}

// recallHistory walks promptHistory by delta. Up (-1) moves toward older
// prompts and Down (1) back toward the newest; Down past the newest restores
// the draft that was in the composer when recall started.
func (m model) recallHistory(delta int) model {
	if len(m.promptHistory) == 0 {
		return m
	}
	if m.historyIndex < 0 {
		if delta < 0 {
			// Drafts keep the expanded text: recalling shows plain content
			// that scrolls normally instead of re-collapsing.
			m.historyDraft = m.expandComposerText()
			m.pastes = nil
			m.historyIndex = len(m.promptHistory) - 1
		} else {
			return m
		}
	} else {
		m.historyIndex += delta
		if m.historyIndex >= len(m.promptHistory) {
			m.historyIndex = -1
			m.input.SetValue(m.historyDraft)
			m.input.CursorEnd()
			m.resizeLayout()
			return m
		}
		m.historyIndex = max(m.historyIndex, 0)
	}
	m.input.SetValue(m.promptHistory[m.historyIndex])
	m.input.CursorEnd()
	m.resizeLayout()
	return m
}

// appendPromptHistory records a submitted prompt, dropping consecutive
// duplicates and keeping at most maximumPromptHistory entries.
func appendPromptHistory(history []string, prompt string) []string {
	if prompt == "" {
		return history
	}
	if count := len(history); count > 0 && history[count-1] == prompt {
		return history
	}
	if len(history) >= maximumPromptHistory {
		history = append(history[:0], history[1:]...)
	}
	return append(history, prompt)
}
