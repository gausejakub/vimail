// Package vimtea provides a Vim-like text editor component for terminal applications
package vimtea

// Cursor represents a position in the text buffer with row and column coordinates
type Cursor struct {
	Row int // Zero-based line index
	Col int // Zero-based column index
}

// Clone creates a copy of the cursor
func (c Cursor) Clone() Cursor {
	return Cursor{Row: c.Row, Col: c.Col}
}

// newCursor creates a new cursor at the specified position
func newCursor(row, col int) Cursor {
	return Cursor{Row: row, Col: col}
}

// wrappedLineHeight returns how many visual rows a buffer line occupies when wrapped.
func (m *editorModel) wrappedLineHeight(rowIdx int) int {
	if !m.wrap || m.width <= 4 {
		return 1
	}
	contentWidth := m.width - 4 // line number width
	if contentWidth <= 0 {
		return 1
	}
	line := m.buffer.Line(rowIdx)
	visLen := visualLength(line, 0)
	if visLen <= contentWidth {
		return 1
	}
	return (visLen + contentWidth - 1) / contentWidth
}

// visualRowOfCursor returns how many visual rows from the top of the viewport
// the cursor occupies, accounting for wrapped lines.
func (m *editorModel) visualRowOfCursor() int {
	rows := 0
	for i := m.viewport.YOffset; i < m.cursor.Row && i < m.buffer.lineCount(); i++ {
		rows += m.wrappedLineHeight(i)
	}
	// Add the sub-row within the cursor's wrapped line
	if m.wrap && m.cursor.Row < m.buffer.lineCount() {
		contentWidth := m.width - 4
		if contentWidth > 0 {
			line := m.buffer.Line(m.cursor.Row)
			cursorVisCol := bufferToVisualPosition(line, m.cursor.Col)
			rows += cursorVisCol / contentWidth
		}
	}
	return rows
}

// ensureCursorVisible scrolls the viewport to make sure the cursor is visible
// This is called whenever the cursor moves or the window is resized
func (m *editorModel) ensureCursorVisible() {
	// If cursor is above the viewport, scroll up
	if m.cursor.Row < m.viewport.YOffset {
		m.viewport.YOffset = m.cursor.Row
	}

	if m.wrap {
		// With wrapping, check if the cursor's visual row fits in the viewport
		for m.viewport.YOffset < m.cursor.Row && m.visualRowOfCursor() >= m.height {
			m.viewport.YOffset++
		}
	} else {
		if m.cursor.Row >= m.viewport.YOffset+m.height {
			// If cursor is below the viewport, scroll down
			m.viewport.YOffset = m.cursor.Row - m.height + 1
		}
	}

	// Horizontal scrolling (only when not wrapping)
	if !m.wrap {
		lineNumWidth := 4
		contentWidth := m.width - lineNumWidth
		if contentWidth > 0 && m.cursor.Row < m.buffer.lineCount() {
			line := m.buffer.Line(m.cursor.Row)
			visualCol := bufferToVisualPosition(line, m.cursor.Col)
			scrollMargin := 8
			if scrollMargin > contentWidth/4 {
				scrollMargin = contentWidth / 4
			}

			if visualCol >= m.xOffset+contentWidth-scrollMargin {
				m.xOffset = visualCol - contentWidth + scrollMargin + 1
			} else if visualCol < m.xOffset+scrollMargin {
				m.xOffset = visualCol - scrollMargin
			}
			if m.xOffset < 0 {
				m.xOffset = 0
			}
		}
	}

	// Ensure cursor is within valid bounds
	m.adjustCursorPosition()
}

// adjustCursorPosition ensures the cursor stays within valid bounds
// Has different behavior based on the current mode (Insert vs Normal/Visual)
func (m *editorModel) adjustCursorPosition() {
	// Keep cursor within valid rows
	if m.cursor.Row < 0 {
		m.cursor.Row = 0
	}
	if m.cursor.Row >= m.buffer.lineCount() {
		m.cursor.Row = m.buffer.lineCount() - 1
	}

	// Adjust column position based on mode
	lineLen := m.buffer.lineLength(m.cursor.Row)
	if m.mode == ModeInsert {
		// In insert mode, cursor can be at end of line
		if m.cursor.Col > lineLen {
			m.cursor.Col = lineLen
		}
	} else {
		// In normal/visual mode, cursor can't be at end of line (except empty lines)
		if lineLen == 0 {
			m.cursor.Col = 0
		} else if m.cursor.Col >= lineLen {
			m.cursor.Col = lineLen - 1
		}
	}

	// Keep cursor within valid columns
	if m.cursor.Col < 0 {
		m.cursor.Col = 0
	}
}
