package vimtea

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// isWhitespace returns true for space and tab only (for WORD motions).
func isWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t'
}

// moveToWordEnd moves the cursor to the end of the current or next word.
// Equivalent to Vim's 'e' motion.
func moveToWordEnd(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col

		// If at or past end of line content, move to next line
		if len(line) == 0 || col >= len(line)-1 {
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				line = model.buffer.Line(model.cursor.Row)
				col = 0
				// Skip leading separators on new line
				for col < len(line) && isWordSeparator(line[col]) {
					col++
				}
				// Find end of this word
				for col < len(line)-1 && !isWordSeparator(line[col+1]) {
					col++
				}
				model.cursor.Col = col
			}
			return
		}

		// Move at least one position forward
		col++

		// Skip separators
		for col < len(line) && isWordSeparator(line[col]) {
			col++
		}
		if col >= len(line) {
			// Wrapped to next line
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				line = model.buffer.Line(model.cursor.Row)
				col = 0
				for col < len(line) && isWordSeparator(line[col]) {
					col++
				}
			} else {
				col = max(0, len(line)-1)
			}
		}

		// Find end of word
		for col < len(line)-1 && !isWordSeparator(line[col+1]) {
			col++
		}

		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToNextWORDStart moves to the start of the next WORD (W).
// WORDs are delimited by whitespace only.
func moveToNextWORDStart(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col + 1

		if col >= len(line) {
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				line = model.buffer.Line(model.cursor.Row)
				// Skip leading whitespace on new line
				col = 0
				for col < len(line) && isWhitespace(line[col]) {
					col++
				}
				model.cursor.Col = col
			}
			return
		}

		// Skip non-whitespace
		for col < len(line) && !isWhitespace(line[col]) {
			col++
		}
		// Skip whitespace
		for col < len(line) && isWhitespace(line[col]) {
			col++
		}
		if col >= len(line) {
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				model.cursor.Col = 0
			} else {
				model.cursor.Col = max(0, len(line)-1)
			}
			return
		}
		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToWORDEnd moves to the end of the current or next WORD (E).
func moveToWORDEnd(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col

		if len(line) == 0 || col >= len(line)-1 {
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				line = model.buffer.Line(model.cursor.Row)
				col = 0
				for col < len(line) && isWhitespace(line[col]) {
					col++
				}
				for col < len(line)-1 && !isWhitespace(line[col+1]) {
					col++
				}
				model.cursor.Col = col
			}
			return
		}

		col++
		// Skip whitespace
		for col < len(line) && isWhitespace(line[col]) {
			col++
		}
		if col >= len(line) {
			if model.cursor.Row < model.buffer.lineCount()-1 {
				model.cursor.Row++
				line = model.buffer.Line(model.cursor.Row)
				col = 0
				for col < len(line) && isWhitespace(line[col]) {
					col++
				}
			} else {
				col = max(0, len(line)-1)
			}
		}
		// Find end of WORD
		for col < len(line)-1 && !isWhitespace(line[col+1]) {
			col++
		}
		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToPrevWORDStart moves to the start of the previous WORD (B).
func moveToPrevWORDStart(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col

		if col <= 0 {
			if model.cursor.Row > 0 {
				model.cursor.Row--
				prevLine := model.buffer.Line(model.cursor.Row)
				model.cursor.Col = max(0, len(prevLine)-1)
			}
			return
		}

		col--
		// Skip whitespace backward
		for col > 0 && isWhitespace(line[col]) {
			col--
		}
		// Skip non-whitespace backward
		for col > 0 && !isWhitespace(line[col-1]) {
			col--
		}
		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToPrevWordEnd moves backward to the end of the previous word (ge).
func moveToPrevWordEnd(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col

		if col <= 0 {
			if model.cursor.Row > 0 {
				model.cursor.Row--
				line = model.buffer.Line(model.cursor.Row)
				col = len(line) - 1
				if col < 0 {
					col = 0
				}
				model.cursor.Col = col
			}
			return
		}

		col--
		// Skip separators backward
		for col >= 0 && isWordSeparator(line[col]) {
			col--
		}
		if col < 0 {
			if model.cursor.Row > 0 {
				model.cursor.Row--
				line = model.buffer.Line(model.cursor.Row)
				col = max(0, len(line)-1)
				for col >= 0 && isWordSeparator(line[col]) {
					col--
				}
			}
			model.cursor.Col = max(0, col)
			return
		}
		// We're on a word char, go to the start of this word then back to end
		// Actually ge goes to end of previous word, so we stay at this position
		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToPrevWORDEnd moves backward to the end of the previous WORD (gE).
func moveToPrevWORDEnd(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		line := model.buffer.Line(model.cursor.Row)
		col := model.cursor.Col

		if col <= 0 {
			if model.cursor.Row > 0 {
				model.cursor.Row--
				line = model.buffer.Line(model.cursor.Row)
				col = max(0, len(line)-1)
				model.cursor.Col = col
			}
			return
		}

		col--
		// Skip whitespace backward
		for col >= 0 && isWhitespace(line[col]) {
			col--
		}
		if col < 0 {
			if model.cursor.Row > 0 {
				model.cursor.Row--
				line = model.buffer.Line(model.cursor.Row)
				col = max(0, len(line)-1)
				for col >= 0 && isWhitespace(line[col]) {
					col--
				}
			}
			model.cursor.Col = max(0, col)
			return
		}
		model.cursor.Col = col
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToLastNonBlank moves to the last non-whitespace character on line (g_).
func moveToLastNonBlank(model *editorModel) tea.Cmd {
	line := model.buffer.Line(model.cursor.Row)
	col := len(line) - 1
	for col >= 0 && (line[col] == ' ' || line[col] == '\t') {
		col--
	}
	model.cursor.Col = max(0, col)
	model.desiredCol = model.cursor.Col
	return nil
}

// moveToMatchingBracket moves to the matching bracket (%).
func moveToMatchingBracket(model *editorModel) tea.Cmd {
	line := model.buffer.Line(model.cursor.Row)
	if model.cursor.Col >= len(line) {
		return nil
	}

	ch := line[model.cursor.Col]
	var target byte
	forward := true

	switch ch {
	case '(':
		target = ')'
	case ')':
		target, forward = '(', false
	case '{':
		target = '}'
	case '}':
		target, forward = '{', false
	case '[':
		target = ']'
	case ']':
		target, forward = '[', false
	default:
		// Scan forward on line for first bracket
		for i := model.cursor.Col; i < len(line); i++ {
			switch line[i] {
			case '(', '{', '[':
				model.cursor.Col = i
				return moveToMatchingBracket(model)
			case ')', '}', ']':
				model.cursor.Col = i
				return moveToMatchingBracket(model)
			}
		}
		return nil
	}

	depth := 1
	row := model.cursor.Row
	col := model.cursor.Col

	if forward {
		col++
		for row < model.buffer.lineCount() {
			l := model.buffer.Line(row)
			for col < len(l) {
				if l[col] == ch {
					depth++
				} else if l[col] == target {
					depth--
					if depth == 0 {
						model.cursor.Row = row
						model.cursor.Col = col
						model.desiredCol = col
						model.ensureCursorVisible()
						return nil
					}
				}
				col++
			}
			row++
			col = 0
		}
	} else {
		col--
		for row >= 0 {
			l := model.buffer.Line(row)
			if col < 0 {
				col = len(l) - 1
			}
			for col >= 0 {
				if l[col] == ch {
					depth++
				} else if l[col] == target {
					depth--
					if depth == 0 {
						model.cursor.Row = row
						model.cursor.Col = col
						model.desiredCol = col
						model.ensureCursorVisible()
						return nil
					}
				}
				col--
			}
			row--
			if row >= 0 {
				col = len(model.buffer.Line(row)) - 1
			}
		}
	}
	return nil
}

// moveToNextParagraph moves forward to the next blank line (}).
func moveToNextParagraph(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		row := model.cursor.Row + 1
		// Skip non-blank lines
		for row < model.buffer.lineCount() && strings.TrimSpace(model.buffer.Line(row)) != "" {
			row++
		}
		// Skip blank lines
		for row < model.buffer.lineCount() && strings.TrimSpace(model.buffer.Line(row)) == "" {
			row++
		}
		// Go back to the last blank line (paragraph boundary)
		if row > 0 {
			row--
		}
		if row >= model.buffer.lineCount() {
			row = model.buffer.lineCount() - 1
		}
		model.cursor.Row = row
		model.cursor.Col = 0
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// moveToPrevParagraph moves backward to the previous blank line ({).
func moveToPrevParagraph(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		row := model.cursor.Row - 1
		// Skip blank lines
		for row > 0 && strings.TrimSpace(model.buffer.Line(row)) == "" {
			row--
		}
		// Skip non-blank lines
		for row > 0 && strings.TrimSpace(model.buffer.Line(row)) != "" {
			row--
		}
		if row < 0 {
			row = 0
		}
		model.cursor.Row = row
		model.cursor.Col = 0
	})
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

// registerMotionBindings registers all new motion bindings.
func registerMotionBindings(m *editorModel) {
	for _, mode := range []EditorMode{ModeNormal, ModeVisual} {
		m.registry.Add("W", moveToNextWORDStart, mode, "Move to next WORD")
		m.registry.Add("E", moveToWORDEnd, mode, "Move to end of WORD")
		m.registry.Add("B", moveToPrevWORDStart, mode, "Move to previous WORD")
		m.registry.Add("ge", moveToPrevWordEnd, mode, "Move to end of previous word")
		m.registry.Add("gE", moveToPrevWORDEnd, mode, "Move to end of previous WORD")
		m.registry.Add("g_", moveToLastNonBlank, mode, "Move to last non-blank")
		m.registry.Add("%", moveToMatchingBracket, mode, "Go to matching bracket")
		m.registry.Add("{", moveToPrevParagraph, mode, "Move to previous paragraph")
		m.registry.Add("}", moveToNextParagraph, mode, "Move to next paragraph")
	}
}
