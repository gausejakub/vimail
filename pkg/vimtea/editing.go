package vimtea

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// joinLines joins current line with next, adding a space and trimming leading whitespace (J).
func joinLinesCmd(m *editorModel) tea.Cmd {
	if m.cursor.Row >= m.buffer.lineCount()-1 {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	line := m.buffer.Line(m.cursor.Row)
	nextLine := strings.TrimLeft(m.buffer.Line(m.cursor.Row+1), " \t")
	joinCol := len(line)
	if len(nextLine) > 0 {
		m.buffer.setLine(m.cursor.Row, line+" "+nextLine)
	} else {
		m.buffer.setLine(m.cursor.Row, line)
	}
	m.buffer.deleteLine(m.cursor.Row + 1)
	m.cursor.Col = joinCol
	m.desiredCol = m.cursor.Col
	return nil
}

// joinLinesNoSpace joins current line with next without adding a space (gJ).
func joinLinesNoSpace(m *editorModel) tea.Cmd {
	if m.cursor.Row >= m.buffer.lineCount()-1 {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	line := m.buffer.Line(m.cursor.Row)
	nextLine := m.buffer.Line(m.cursor.Row + 1)
	joinCol := len(line)
	m.buffer.setLine(m.cursor.Row, line+nextLine)
	m.buffer.deleteLine(m.cursor.Row + 1)
	m.cursor.Col = joinCol
	m.desiredCol = m.cursor.Col
	return nil
}

// substituteChar deletes char at cursor and enters insert mode (s).
func substituteChar(m *editorModel) tea.Cmd {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) > 0 && m.cursor.Col < len(line) {
		m.buffer.saveUndoState(m.cursor)
		m.buffer.setLine(m.cursor.Row, line[:m.cursor.Col]+line[m.cursor.Col+1:])
	}
	return switchMode(m, ModeInsert)
}

// substituteLine clears the line and enters insert mode (S).
func substituteLine(m *editorModel) tea.Cmd {
	return changeLine(m)
}

// toggleCase toggles the case of the char at cursor and advances (~ in normal).
func toggleCase(m *editorModel) tea.Cmd {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 || m.cursor.Col >= len(line) {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	ch := rune(line[m.cursor.Col])
	var toggled rune
	if unicode.IsUpper(ch) {
		toggled = unicode.ToLower(ch)
	} else {
		toggled = unicode.ToUpper(ch)
	}
	m.buffer.setLine(m.cursor.Row, line[:m.cursor.Col]+string(toggled)+line[m.cursor.Col+1:])
	if m.cursor.Col < len(line)-1 {
		m.cursor.Col++
	}
	m.desiredCol = m.cursor.Col
	return nil
}

// toggleCaseVisual toggles case of the visual selection (~ in visual).
func toggleCaseVisual(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	start, end := m.GetSelectionBoundary()
	for row := start.Row; row <= end.Row; row++ {
		line := m.buffer.Line(row)
		startCol := 0
		if row == start.Row {
			startCol = start.Col
		}
		endCol := len(line)
		if row == end.Row {
			endCol = min(end.Col+1, len(line))
		}
		var sb strings.Builder
		sb.WriteString(line[:startCol])
		for _, ch := range line[startCol:endCol] {
			if unicode.IsUpper(ch) {
				sb.WriteRune(unicode.ToLower(ch))
			} else {
				sb.WriteRune(unicode.ToUpper(ch))
			}
		}
		sb.WriteString(line[endCol:])
		m.buffer.setLine(row, sb.String())
	}
	m.cursor = start
	return switchMode(m, ModeNormal)
}

// lowercaseVisual lowercases the visual selection (u in visual).
func lowercaseVisual(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	start, end := m.GetSelectionBoundary()
	for row := start.Row; row <= end.Row; row++ {
		line := m.buffer.Line(row)
		startCol := 0
		if row == start.Row {
			startCol = start.Col
		}
		endCol := len(line)
		if row == end.Row {
			endCol = min(end.Col+1, len(line))
		}
		m.buffer.setLine(row, line[:startCol]+strings.ToLower(line[startCol:endCol])+line[endCol:])
	}
	m.cursor = start
	return switchMode(m, ModeNormal)
}

// uppercaseVisual uppercases the visual selection (U in visual).
func uppercaseVisual(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	start, end := m.GetSelectionBoundary()
	for row := start.Row; row <= end.Row; row++ {
		line := m.buffer.Line(row)
		startCol := 0
		if row == start.Row {
			startCol = start.Col
		}
		endCol := len(line)
		if row == end.Row {
			endCol = min(end.Col+1, len(line))
		}
		m.buffer.setLine(row, line[:startCol]+strings.ToUpper(line[startCol:endCol])+line[endCol:])
	}
	m.cursor = start
	return switchMode(m, ModeNormal)
}

// indentLine prepends a tab to the current line (>>).
func indentLine(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	line := m.buffer.Line(m.cursor.Row)
	m.buffer.setLine(m.cursor.Row, "\t"+line)
	m.cursor.Col++
	m.desiredCol = m.cursor.Col
	m.keySequence = []string{}
	return nil
}

// deindentLine removes leading tab or spaces from current line (<<).
func deindentLine(m *editorModel) tea.Cmd {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 {
		m.keySequence = []string{}
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	if line[0] == '\t' {
		m.buffer.setLine(m.cursor.Row, line[1:])
		if m.cursor.Col > 0 {
			m.cursor.Col--
		}
	} else if line[0] == ' ' {
		// Remove up to tabWidth spaces
		removed := 0
		for removed < tabWidth && removed < len(line) && line[removed] == ' ' {
			removed++
		}
		m.buffer.setLine(m.cursor.Row, line[removed:])
		m.cursor.Col = max(0, m.cursor.Col-removed)
	}
	m.desiredCol = m.cursor.Col
	m.keySequence = []string{}
	return nil
}

// indentVisual indents selected lines (> in visual).
func indentVisual(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	start, end := m.GetSelectionBoundary()
	for row := start.Row; row <= end.Row; row++ {
		line := m.buffer.Line(row)
		m.buffer.setLine(row, "\t"+line)
	}
	m.cursor = start
	return switchMode(m, ModeNormal)
}

// deindentVisual deindents selected lines (< in visual).
func deindentVisual(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)
	start, end := m.GetSelectionBoundary()
	for row := start.Row; row <= end.Row; row++ {
		line := m.buffer.Line(row)
		if len(line) > 0 && line[0] == '\t' {
			m.buffer.setLine(row, line[1:])
		} else if len(line) > 0 && line[0] == ' ' {
			removed := 0
			for removed < tabWidth && removed < len(line) && line[removed] == ' ' {
				removed++
			}
			m.buffer.setLine(row, line[removed:])
		}
	}
	m.cursor = start
	return switchMode(m, ModeNormal)
}

// visualMoveOtherEnd swaps cursor and visualStart (o in visual).
func visualMoveOtherEnd(m *editorModel) tea.Cmd {
	m.cursor, m.visualStart = m.visualStart, m.cursor
	m.desiredCol = m.cursor.Col
	m.ensureCursorVisible()
	return nil
}

// insertDeleteWord deletes the word backward in insert mode (Ctrl+w).
func insertDeleteWord(m *editorModel) tea.Cmd {
	if m.cursor.Col == 0 {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	line := m.buffer.Line(m.cursor.Row)
	col := m.cursor.Col
	// Skip whitespace backward
	for col > 0 && (line[col-1] == ' ' || line[col-1] == '\t') {
		col--
	}
	// Skip non-whitespace backward
	for col > 0 && line[col-1] != ' ' && line[col-1] != '\t' {
		col--
	}
	m.buffer.setLine(m.cursor.Row, line[:col]+line[m.cursor.Col:])
	m.cursor.Col = col
	return nil
}

// registerEditingBindings registers all editing command bindings.
func registerEditingBindings(m *editorModel) {
	// Normal mode
	m.registry.Add("J", joinLinesCmd, ModeNormal, "Join lines")
	m.registry.Add("gJ", joinLinesNoSpace, ModeNormal, "Join lines without space")
	m.registry.Add("s", substituteChar, ModeNormal, "Substitute character")
	m.registry.Add("S", substituteLine, ModeNormal, "Substitute line")
	m.registry.Add("~", toggleCase, ModeNormal, "Toggle case")
	m.registry.Add(">>", indentLine, ModeNormal, "Indent line")
	m.registry.Add("<<", deindentLine, ModeNormal, "Deindent line")

	// Visual mode
	m.registry.Add("~", toggleCaseVisual, ModeVisual, "Toggle case")
	m.registry.Add("u", lowercaseVisual, ModeVisual, "Lowercase selection")
	m.registry.Add("U", uppercaseVisual, ModeVisual, "Uppercase selection")
	m.registry.Add(">", indentVisual, ModeVisual, "Indent selection")
	m.registry.Add("<", deindentVisual, ModeVisual, "Deindent selection")
	m.registry.Add("o", visualMoveOtherEnd, ModeVisual, "Move to other end of selection")

	// Insert mode
	m.registry.Add("ctrl+w", insertDeleteWord, ModeInsert, "Delete word backward")
}
