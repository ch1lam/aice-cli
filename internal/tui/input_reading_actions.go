package tui

const (
	inputActionReadingQuit           inputAction = "reading.quit"
	inputActionReadingClose          inputAction = "reading.close"
	inputActionReadingBody           inputAction = "reading.body"
	inputActionReadingSelectPrevious inputAction = "reading.select.previous"
	inputActionReadingSelectNext     inputAction = "reading.select.next"
	inputActionReadingSelectPageUp   inputAction = "reading.select.page-up"
	inputActionReadingSelectPageDown inputAction = "reading.select.page-down"
	inputActionReadingJump           inputAction = "reading.jump"
	inputActionReadingDirectory      inputAction = "reading.directory"
	inputActionReadingScrollUp       inputAction = "reading.scroll.up"
	inputActionReadingScrollDown     inputAction = "reading.scroll.down"
	inputActionReadingPageUp         inputAction = "reading.page.up"
	inputActionReadingPageDown       inputAction = "reading.page.down"
	inputActionReadingCode           inputAction = "reading.code"
	inputActionReadingTop            inputAction = "reading.top"
	inputActionReadingLatest         inputAction = "reading.latest"
	inputActionReadingResume         inputAction = "reading.resume"
)

func (m model) readingInputBindings() []inputBinding {
	if m.inputContext().focus == inputFocusDirectory {
		bindings := []inputBinding{
			actionBinding(inputActionReadingJump, "Enter", "jump to question", "enter"),
			actionBinding(inputActionReadingBody, "Esc/T/Ctrl+t", "back", "esc", "t", "ctrl+t"),
			actionBinding(inputActionReadingSelectPrevious, "", "", "up", "k"),
			actionBinding(inputActionReadingSelectNext, "", "", "down", "j"),
			actionBinding(inputActionReadingSelectPageUp, "", "", "pgup"),
			actionBinding(inputActionReadingSelectPageDown, "", "", "pgdown"),
			actionBinding(inputActionReadingClose, "Ctrl+c", "close history", "ctrl+c"),
			actionBinding(inputActionReadingQuit, "Ctrl+d", "quit", "ctrl+d"),
		}
		for i := range bindings {
			switch bindings[i].action {
			case inputActionReadingJump, inputActionReadingSelectPrevious, inputActionReadingSelectNext,
				inputActionReadingSelectPageUp, inputActionReadingSelectPageDown:
				bindings[i].binding.SetEnabled(len(m.reading.turns) > 0)
			}
			switch bindings[i].action {
			case inputActionReadingSelectPageUp, inputActionReadingSelectPageDown,
				inputActionReadingClose, inputActionReadingQuit:
				bindings[i].short = false
			}
		}
		return bindings
	}
	resume := "resume session"
	if m.reading.previous == nil || m.reading.previous.sessionPicker == nil {
		resume = "return to conversation"
	}
	bindings := []inputBinding{
		actionBinding(inputActionReadingResume, "Enter", resume, "enter"),
		actionBinding(inputActionReadingClose, "Esc/Ctrl+c", "back", "esc", "ctrl+c"),
		actionBinding(inputActionReadingDirectory, "T/Ctrl+t", "questions", "t", "ctrl+t"),
		actionBinding(inputActionReadingScrollUp, "", "", "up", "k"),
		actionBinding(inputActionReadingScrollDown, "", "", "down", "j"),
		actionBinding(inputActionReadingCode, "C/Alt+o", "toggle code", "c", "alt+o"),
		actionBinding(inputActionReadingLatest, "End", "latest", "end"),
		actionBinding(inputActionReadingPageUp, "", "", "pgup"),
		actionBinding(inputActionReadingPageDown, "", "", "pgdown", "space", " "),
		actionBinding(inputActionReadingTop, "", "", "home"),
		actionBinding(inputActionReadingQuit, "Ctrl+d", "quit", "ctrl+d"),
	}
	for i := range bindings {
		switch bindings[i].action {
		case inputActionReadingPageUp, inputActionReadingPageDown, inputActionReadingTop, inputActionReadingQuit:
			bindings[i].short = false
		}
	}
	return bindings
}
