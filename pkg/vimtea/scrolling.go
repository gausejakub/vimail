package vimtea

import tea "github.com/charmbracelet/bubbletea"

// scrollDownHalf scrolls down half a page (Ctrl+d).
func scrollDownHalf(m *editorModel) tea.Cmd {
	half := max(1, m.height/2)
	m.cursor.Row = min(m.cursor.Row+half, m.buffer.lineCount()-1)
	m.viewport.YOffset = min(m.viewport.YOffset+half, max(0, m.buffer.lineCount()-m.height))
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	m.ensureCursorVisible()
	return nil
}

// scrollUpHalf scrolls up half a page (Ctrl+u).
func scrollUpHalf(m *editorModel) tea.Cmd {
	half := max(1, m.height/2)
	m.cursor.Row = max(m.cursor.Row-half, 0)
	m.viewport.YOffset = max(m.viewport.YOffset-half, 0)
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	m.ensureCursorVisible()
	return nil
}

// scrollDownFull scrolls down a full page (Ctrl+f).
func scrollDownFull(m *editorModel) tea.Cmd {
	page := max(1, m.height-2) // Leave 2 lines overlap like Vim
	m.cursor.Row = min(m.cursor.Row+page, m.buffer.lineCount()-1)
	m.viewport.YOffset = min(m.viewport.YOffset+page, max(0, m.buffer.lineCount()-m.height))
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	m.ensureCursorVisible()
	return nil
}

// scrollUpFull scrolls up a full page (Ctrl+b).
func scrollUpFull(m *editorModel) tea.Cmd {
	page := max(1, m.height-2)
	m.cursor.Row = max(m.cursor.Row-page, 0)
	m.viewport.YOffset = max(m.viewport.YOffset-page, 0)
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	m.ensureCursorVisible()
	return nil
}

// scrollDownLine scrolls the viewport down one line (Ctrl+e).
// Pushes cursor down if it goes above the viewport.
func scrollDownLine(m *editorModel) tea.Cmd {
	maxOffset := max(0, m.buffer.lineCount()-m.height)
	if m.viewport.YOffset < maxOffset {
		m.viewport.YOffset++
		// Push cursor if it's above the viewport
		if m.cursor.Row < m.viewport.YOffset {
			m.cursor.Row = m.viewport.YOffset
			m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
		}
	}
	return nil
}

// scrollUpLine scrolls the viewport up one line (Ctrl+y).
// Pushes cursor up if it goes below the viewport.
func scrollUpLine(m *editorModel) tea.Cmd {
	if m.viewport.YOffset > 0 {
		m.viewport.YOffset--
		// Push cursor if it's below the viewport
		bottomVisible := m.viewport.YOffset + m.height - 1
		if m.cursor.Row > bottomVisible {
			m.cursor.Row = bottomVisible
			m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
		}
	}
	return nil
}

// scrollCenterCursor centers the viewport on the cursor line (zz).
func scrollCenterCursor(m *editorModel) tea.Cmd {
	m.viewport.YOffset = max(0, m.cursor.Row-m.height/2)
	maxOffset := max(0, m.buffer.lineCount()-m.height)
	if m.viewport.YOffset > maxOffset {
		m.viewport.YOffset = maxOffset
	}
	return nil
}

// scrollCursorTop scrolls so cursor is at top of viewport (zt).
func scrollCursorTop(m *editorModel) tea.Cmd {
	m.viewport.YOffset = max(0, m.cursor.Row)
	maxOffset := max(0, m.buffer.lineCount()-m.height)
	if m.viewport.YOffset > maxOffset {
		m.viewport.YOffset = maxOffset
	}
	return nil
}

// scrollCursorBottom scrolls so cursor is at bottom of viewport (zb).
func scrollCursorBottom(m *editorModel) tea.Cmd {
	m.viewport.YOffset = max(0, m.cursor.Row-m.height+1)
	return nil
}

// moveCursorToScreenTop moves cursor to top of visible screen (H).
func moveCursorToScreenTop(m *editorModel) tea.Cmd {
	m.cursor.Row = m.viewport.YOffset
	if m.cursor.Row >= m.buffer.lineCount() {
		m.cursor.Row = m.buffer.lineCount() - 1
	}
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	return nil
}

// moveCursorToScreenMiddle moves cursor to middle of visible screen (M).
func moveCursorToScreenMiddle(m *editorModel) tea.Cmd {
	m.cursor.Row = m.viewport.YOffset + m.height/2
	if m.cursor.Row >= m.buffer.lineCount() {
		m.cursor.Row = m.buffer.lineCount() - 1
	}
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	return nil
}

// moveCursorToScreenBottom moves cursor to bottom of visible screen (L).
func moveCursorToScreenBottom(m *editorModel) tea.Cmd {
	m.cursor.Row = m.viewport.YOffset + m.height - 1
	if m.cursor.Row >= m.buffer.lineCount() {
		m.cursor.Row = m.buffer.lineCount() - 1
	}
	m.cursor.Col = min(m.desiredCol, max(0, m.buffer.lineLength(m.cursor.Row)-1))
	return nil
}

// registerScrollBindings registers all scroll and viewport bindings.
func registerScrollBindings(m *editorModel) {
	for _, mode := range []EditorMode{ModeNormal, ModeVisual} {
		m.registry.Add("ctrl+d", scrollDownHalf, mode, "Scroll down half page")
		m.registry.Add("ctrl+u", scrollUpHalf, mode, "Scroll up half page")
		m.registry.Add("ctrl+f", scrollDownFull, mode, "Scroll down full page")
		m.registry.Add("ctrl+b", scrollUpFull, mode, "Scroll up full page")
		m.registry.Add("ctrl+e", scrollDownLine, mode, "Scroll down one line")
		m.registry.Add("ctrl+y", scrollUpLine, mode, "Scroll up one line")
		m.registry.Add("zz", scrollCenterCursor, mode, "Center cursor on screen")
		m.registry.Add("zt", scrollCursorTop, mode, "Scroll cursor to top")
		m.registry.Add("zb", scrollCursorBottom, mode, "Scroll cursor to bottom")
		m.registry.Add("H", moveCursorToScreenTop, mode, "Move to screen top")
		m.registry.Add("M", moveCursorToScreenMiddle, mode, "Move to screen middle")
		m.registry.Add("L", moveCursorToScreenBottom, mode, "Move to screen bottom")
	}
}
