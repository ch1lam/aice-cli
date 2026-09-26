package tui

import tea "charm.land/bubbletea/v2"

// Capture owns the gesture lifetime; existing typed targets retain their
// payloads (including the frozen selection). Content updates alone do not
// invalidate a drag. Click handlers separately revalidate their live target.
type pointerCapture struct {
	active                         bool
	origin                         tea.Mouse
	layout                         screenLayout
	picker                         sessionPickerLayout
	workspace                      string
	workspaceBounds, contextBounds screenRect
}

func (m *model) cancelPointer() {
	m.capture = pointerCapture{}
	m.workspacePress = nil
	m.contextPressed = false
	m.settingsLauncherPress = ""
	m.selection.clear()
	if p := m.settings; p != nil {
		p.pressed = ""
	}
	if p := m.sessionPicker; p != nil {
		p.closePressed = false
		p.copyPressedID = ""
	}
}

func (m *model) beginInputEvent(message tea.Msg) {
	switch event := message.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg:
		m.cancelPointer()
	case tea.BlurMsg:
		m.cancelPointer()
		m.clearQuitPending = false
		if m.sessionPicker != nil {
			m.sessionPicker.pointer = nil
		}
	case tea.MouseWheelMsg:
		if m.selection.active {
			// A transcript drag owns the screen: the wheel browses the
			// frozen version and re-hits the focus instead of cancelling
			// the gesture. Other buttons, the picker and modals keep
			// their existing cancel behaviour. The wheel also revokes
			// click/fold eligibility (selection.wheeled) without clearing
			// the text range.
			m.clearQuitPending = false
		} else {
			m.cancelPointer()
			m.clearQuitPending = false
		}
	case tea.MouseClickMsg:
		m.cancelPointer()
		m.clearQuitPending = false
		if event.Button == tea.MouseLeft {
			m.capture = pointerCapture{active: true, origin: event.Mouse(), layout: m.screenLayout(), workspace: m.workingDirectory, workspaceBounds: m.workspaceRect(), contextBounds: m.contextRect()}
			if m.sessionPicker != nil {
				m.capture.picker = m.sessionPickerLayout()
			}
		}
	case tea.MouseMotionMsg:
		if m.capture.active && !m.selection.active && (event.X != m.capture.origin.X || event.Y != m.capture.origin.Y) {
			m.cancelPointer()
		}
	case tea.MouseReleaseMsg:
		if m.capture.active && event.Button != m.capture.origin.Button {
			m.cancelPointer()
		}
	}
}

func (m *model) finishPointerEvent(message tea.Msg) {
	if _, released := message.(tea.MouseReleaseMsg); released {
		m.cancelPointer()
		return
	}
	if !m.capture.active {
		return
	}
	if m.capture.layout != m.screenLayout() || m.capture.workspace != m.workingDirectory ||
		(m.sessionPicker != nil && m.capture.picker != m.sessionPickerLayout()) ||
		(m.workspacePress != nil && m.capture.workspaceBounds != m.workspaceRect()) ||
		(m.contextPressed && m.capture.contextBounds != m.contextRect()) {
		m.cancelPointer()
	}
}

func (m model) routePointer(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.settingsVisible() {
		return m.settingsPointer(message)
	}
	if m.sessionPicker != nil {
		if _, blurred := message.(tea.BlurMsg); !blurred {
			return m.handleSessionPicker(message)
		}
	}
	switch message := message.(type) {
	case tea.MouseClickMsg:
		if target := m.settingsLauncher(message.Mouse()); target != "" && message.Button == tea.MouseLeft {
			m.settingsLauncherPress = target
			return m, nil
		}
		m.trackPointer(message.Mouse())
		m.workspacePress = nil
		if message.Button == tea.MouseLeft && m.workspaceContains(message.Mouse()) {
			mouse := message.Mouse()
			m.workspacePress = &mouse
			m.selection.clear()
			m.composerActive = false
			return m, nil
		}
		if message.Button == tea.MouseLeft {
			m.contextPressed = m.contextContains(message.Mouse())
			if m.contextPressed {
				m.selection.clear()
				m.composerActive = false
				return m, nil
			}
		}
		if message.Button == tea.MouseLeft {
			m.composerActive = m.composerContains(message.Mouse(), m.layoutWidth())
		}
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseClick(message); handled {
			return updated, command
		}
	case tea.MouseMotionMsg:
		m.trackPointer(message.Mouse())
		if press := m.workspacePress; press != nil && (press.X != message.X || press.Y != message.Y) {
			m.workspacePress = nil
		}
		m.contextPressed = false
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseMotion(message); handled {
			return updated, command
		}
		return m, nil
	case tea.MouseReleaseMsg:
		if target := m.settingsLauncherPress; target != "" {
			m.settingsLauncherPress = ""
			if message.Button == tea.MouseLeft && m.settingsLauncher(message.Mouse()) == target {
				var next model
				var cmd tea.Cmd
				if target == "usage" {
					next, cmd, _ = m.openUsage(0)
				} else {
					next, cmd, _ = m.openSettings()
				}
				return next, cmd
			}
			return m, nil
		}
		m.trackPointer(message.Mouse())
		if press := m.workspacePress; press != nil && message.Button == tea.MouseLeft {
			m.workspacePress = nil
			if press.X == message.X && press.Y == message.Y && m.workspaceContains(message.Mouse()) {
				command := m.activateWorkspace(press.Mod)
				return m, command
			}
			return m, nil
		}
		if m.contextPressed && message.Button == tea.MouseLeft {
			m.contextPressed = false
			if m.contextContains(message.Mouse()) {
				m.contextShowFraction = m.contextFractionVisible()
				m.contextHoverConfirmed = true
			}
			return m, nil
		}
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseRelease(message); handled {
			return updated, command
		}
	case tea.MouseWheelMsg:
		if message.Button != tea.MouseWheelUp && message.Button != tea.MouseWheelDown {
			return m, nil
		}
		if message.X < 0 || message.Y < 0 || message.X >= m.width || message.Y >= m.height {
			return m, nil
		}
		m.workspacePress = nil
		m.contextPressed = false
		m.trackPointer(message.Mouse())
		if m.selection.active {
			// Drag gesture: scroll the frozen version, re-hit the focus
			// at the current mouse position and repaint only the new
			// window. Never settle deferred streaming layout here and
			// never touch the live viewport anchor; the release restores
			// it from the frozen anchor.
			m.handleSelectionWheel(message)
			return m, nil
		}
		m.selection.clear()
		// Scrolling needs fresh items; a deferred gesture may have skipped
		// their rebuild, so settle before moving the anchor.
		m.settleDeferredViewport()
		if m.guardPending != nil {
			var command tea.Cmd
			m.guardViewport, command = m.guardViewport.Update(message)
			return m, command
		}
		// Scrolling changes only the transcript anchor. Routing wheel events
		// through the composer also rebuilds its layout and completion state.
		var command tea.Cmd
		m.viewport, command = m.viewport.Update(message)
		return m, command
	case tea.BlurMsg:
		m.workspacePress = nil
		m.contextPressed = false
		m.contextHoverConfirmed = false
		m.pointer = transcriptPointer{}
		m.composerActive = false
		m.selection.clear()
		return m, nil
	}
	// Mouse messages never enter the composer or its completion machinery.
	return m, nil
}
