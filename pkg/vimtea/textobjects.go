package vimtea

import tea "github.com/charmbracelet/bubbletea"

// textObjectRange computes the start and end cursors for a text object.
// Returns ok=false if the text object cannot be found.
type textObjectRange func(m *editorModel) (start Cursor, end Cursor, ok bool)

// innerWord returns the range of the word under cursor (iw).
func innerWord(m *editorModel) (Cursor, Cursor, bool) {
	s, e := getWordBoundary(m)
	if s == e {
		return Cursor{}, Cursor{}, false
	}
	return Cursor{Row: m.cursor.Row, Col: s}, Cursor{Row: m.cursor.Row, Col: e - 1}, true
}

// aroundWord returns the range of the word under cursor plus surrounding whitespace (aw).
func aroundWord(m *editorModel) (Cursor, Cursor, bool) {
	s, e := getWordBoundary(m)
	if s == e {
		return Cursor{}, Cursor{}, false
	}
	line := m.buffer.Line(m.cursor.Row)
	// Try trailing whitespace first
	end := e
	for end < len(line) && (line[end] == ' ' || line[end] == '\t') {
		end++
	}
	start := s
	if end == e {
		// No trailing whitespace, try leading whitespace
		for start > 0 && (line[start-1] == ' ' || line[start-1] == '\t') {
			start--
		}
	}
	return Cursor{Row: m.cursor.Row, Col: start}, Cursor{Row: m.cursor.Row, Col: end - 1}, true
}

// innerWORD returns the range of the WORD under cursor (iW).
func innerWORD(m *editorModel) (Cursor, Cursor, bool) {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 {
		return Cursor{}, Cursor{}, false
	}
	col := m.cursor.Col
	if col >= len(line) {
		col = len(line) - 1
	}
	if isWhitespace(line[col]) {
		return Cursor{}, Cursor{}, false
	}
	start := col
	for start > 0 && !isWhitespace(line[start-1]) {
		start--
	}
	end := col
	for end < len(line)-1 && !isWhitespace(line[end+1]) {
		end++
	}
	return Cursor{Row: m.cursor.Row, Col: start}, Cursor{Row: m.cursor.Row, Col: end}, true
}

// aroundWORD returns the range of the WORD under cursor plus surrounding whitespace (aW).
func aroundWORD(m *editorModel) (Cursor, Cursor, bool) {
	start, end, ok := innerWORD(m)
	if !ok {
		return Cursor{}, Cursor{}, false
	}
	line := m.buffer.Line(m.cursor.Row)
	// Try trailing whitespace
	e := end.Col + 1
	for e < len(line) && isWhitespace(line[e]) {
		e++
	}
	s := start.Col
	if e == end.Col+1 {
		// No trailing whitespace, try leading
		for s > 0 && isWhitespace(line[s-1]) {
			s--
		}
	}
	return Cursor{Row: m.cursor.Row, Col: s}, Cursor{Row: m.cursor.Row, Col: e - 1}, true
}

// findUnmatchedOpen scans backward from cursor for an unmatched open bracket.
func findUnmatchedOpen(m *editorModel, open, close byte) (Cursor, bool) {
	row := m.cursor.Row
	col := m.cursor.Col - 1
	depth := 1

	for row >= 0 {
		line := m.buffer.Line(row)
		if col < 0 || col >= len(line) {
			col = len(line) - 1
		}
		for col >= 0 {
			if line[col] == close {
				depth++
			} else if line[col] == open {
				depth--
				if depth == 0 {
					return Cursor{Row: row, Col: col}, true
				}
			}
			col--
		}
		row--
		if row >= 0 {
			col = len(m.buffer.Line(row)) - 1
		}
	}
	return Cursor{}, false
}

// findMatchingClose scans forward from a position for the matching close bracket.
func findMatchingClose(m *editorModel, fromRow, fromCol int, open, close byte) (Cursor, bool) {
	row := fromRow
	col := fromCol + 1
	depth := 1

	for row < m.buffer.lineCount() {
		line := m.buffer.Line(row)
		for col < len(line) {
			if line[col] == open {
				depth++
			} else if line[col] == close {
				depth--
				if depth == 0 {
					return Cursor{Row: row, Col: col}, true
				}
			}
			col++
		}
		row++
		col = 0
	}
	return Cursor{}, false
}

// innerBlock returns the range between matched delimiters, excluding them.
func innerBlock(open, close byte) textObjectRange {
	return func(m *editorModel) (Cursor, Cursor, bool) {
		// Check if cursor is on the open bracket itself
		line := m.buffer.Line(m.cursor.Row)
		var openPos Cursor
		var found bool

		if m.cursor.Col < len(line) && line[m.cursor.Col] == open {
			openPos = Cursor{Row: m.cursor.Row, Col: m.cursor.Col}
			found = true
		} else {
			openPos, found = findUnmatchedOpen(m, open, close)
		}
		if !found {
			return Cursor{}, Cursor{}, false
		}

		closePos, found := findMatchingClose(m, openPos.Row, openPos.Col, open, close)
		if !found {
			return Cursor{}, Cursor{}, false
		}

		// Inner = one past open to one before close
		start := Cursor{Row: openPos.Row, Col: openPos.Col + 1}
		end := Cursor{Row: closePos.Row, Col: closePos.Col - 1}

		// Handle case where open and close are on the same line next to each other
		if start.Row == end.Row && start.Col > end.Col {
			return Cursor{}, Cursor{}, false
		}
		// If start wraps to next line
		if start.Col >= len(m.buffer.Line(start.Row)) {
			start.Row++
			start.Col = 0
		}
		if end.Col < 0 {
			end.Row--
			if end.Row >= 0 {
				end.Col = max(0, len(m.buffer.Line(end.Row))-1)
			}
		}

		return start, end, true
	}
}

// aroundBlock returns the range including matched delimiters.
func aroundBlock(open, close byte) textObjectRange {
	return func(m *editorModel) (Cursor, Cursor, bool) {
		line := m.buffer.Line(m.cursor.Row)
		var openPos Cursor
		var found bool

		if m.cursor.Col < len(line) && line[m.cursor.Col] == open {
			openPos = Cursor{Row: m.cursor.Row, Col: m.cursor.Col}
			found = true
		} else {
			openPos, found = findUnmatchedOpen(m, open, close)
		}
		if !found {
			return Cursor{}, Cursor{}, false
		}

		closePos, found := findMatchingClose(m, openPos.Row, openPos.Col, open, close)
		if !found {
			return Cursor{}, Cursor{}, false
		}

		return openPos, closePos, true
	}
}

// deleteTextObject deletes the range returned by a text object.
func deleteTextObject(m *editorModel, rangeFn textObjectRange) tea.Cmd {
	start, end, ok := rangeFn(m)
	if !ok {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	m.yankBuffer = m.buffer.deleteRange(start, end)
	m.cursor = start
	m.ensureCursorVisible()
	m.keySequence = []string{}
	return nil
}

// changeTextObject deletes the range and enters insert mode.
func changeTextObject(m *editorModel, rangeFn textObjectRange) tea.Cmd {
	start, end, ok := rangeFn(m)
	if !ok {
		return nil
	}
	m.buffer.saveUndoState(m.cursor)
	m.yankBuffer = m.buffer.deleteRange(start, end)
	m.cursor = start
	m.ensureCursorVisible()
	m.keySequence = []string{}
	return switchMode(m, ModeInsert)
}

// yankTextObject yanks the range returned by a text object.
func yankTextObject(m *editorModel, rangeFn textObjectRange) tea.Cmd {
	start, end, ok := rangeFn(m)
	if !ok {
		return nil
	}
	text := m.buffer.getRange(start, end)
	setupYankHighlight(m, start, end, text, false)
	m.keySequence = []string{}
	return nil
}

// textObjectEntry defines a text object with its key and range function.
type textObjectEntry struct {
	key     string
	rangeFn textObjectRange
}

// registerTextObjectBindings registers all text object combos table-driven.
func registerTextObjectBindings(m *editorModel) {
	objects := []textObjectEntry{
		{"iw", innerWord},
		{"aw", aroundWord},
		{"iW", innerWORD},
		{"aW", aroundWORD},
		{"ib", innerBlock('(', ')')},
		{"i(", innerBlock('(', ')')},
		{"i)", innerBlock('(', ')')},
		{"ab", aroundBlock('(', ')')},
		{"a(", aroundBlock('(', ')')},
		{"a)", aroundBlock('(', ')')},
		{"iB", innerBlock('{', '}')},
		{"i{", innerBlock('{', '}')},
		{"i}", innerBlock('{', '}')},
		{"aB", aroundBlock('{', '}')},
		{"a{", aroundBlock('{', '}')},
		{"a}", aroundBlock('{', '}')},
		{"i[", innerBlock('[', ']')},
		{"i]", innerBlock('[', ']')},
		{"a[", aroundBlock('[', ']')},
		{"a]", aroundBlock('[', ']')},
	}

	for _, obj := range objects {
		rangeFn := obj.rangeFn
		key := obj.key
		m.registry.Add("d"+key, func(model *editorModel) tea.Cmd {
			return deleteTextObject(model, rangeFn)
		}, ModeNormal, "Delete "+key)
		m.registry.Add("c"+key, func(model *editorModel) tea.Cmd {
			return changeTextObject(model, rangeFn)
		}, ModeNormal, "Change "+key)
		m.registry.Add("y"+key, func(model *editorModel) tea.Cmd {
			return yankTextObject(model, rangeFn)
		}, ModeNormal, "Yank "+key)
	}
}
