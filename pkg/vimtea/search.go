package vimtea

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleSearchInput processes keypresses while in search mode.
func (m *editorModel) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.searchMode = false
		m.searchBuffer = ""
		m.statusMessage = ""
		return m, nil
	case "enter":
		m.searchMode = false
		m.searchPattern = m.searchBuffer
		m.searchBuffer = ""
		if m.searchPattern != "" {
			if m.searchForward {
				searchNext(m)
			} else {
				searchPrev(m)
			}
		}
		return m, nil
	case "backspace":
		if len(m.searchBuffer) > 0 {
			m.searchBuffer = m.searchBuffer[:len(m.searchBuffer)-1]
		} else {
			m.searchMode = false
			m.statusMessage = ""
		}
		return m, nil
	default:
		if len(key) == 1 {
			m.searchBuffer += key
		}
		return m, nil
	}
}

// searchNext finds the next occurrence of the search pattern after the
// cursor, wrapping through the end of the document and back through the
// part of the cursor's own line before the cursor ('wrapscan').
func searchNext(m *editorModel) {
	if m.searchPattern == "" {
		return
	}

	row := m.cursor.Row
	col := m.cursor.Col + 1
	lineCount := m.buffer.lineCount()

	// One extra iteration revisits the starting line after the wrap so a
	// match earlier on the same line is found too. If the only match is
	// the one under the cursor, the search lands on it again (Vim's
	// wrapscan behaves the same way).
	for i := 0; i <= lineCount; i++ {
		lineIdx := (row + i) % lineCount
		line := m.buffer.Line(lineIdx)

		startCol := 0
		if i == 0 {
			startCol = col
		}

		if startCol <= len(line) {
			idx := strings.Index(line[startCol:], m.searchPattern)
			if idx >= 0 {
				m.cursor.Row = lineIdx
				m.cursor.Col = startCol + idx
				m.desiredCol = m.cursor.Col
				m.ensureCursorVisible()
				m.statusMessage = "/" + m.searchPattern
				return
			}
		}
	}
	m.statusMessage = "Pattern not found: " + m.searchPattern
}

// searchPrev finds the closest occurrence of the search pattern that
// starts before the cursor, wrapping through the start of the document.
// A match may extend up to or past the cursor position: ?abc with the
// cursor on the trailing c of "abc" finds that occurrence.
func searchPrev(m *editorModel) {
	if m.searchPattern == "" {
		return
	}

	row := m.cursor.Row
	col := m.cursor.Col
	lineCount := m.buffer.lineCount()

	for i := 0; i <= lineCount; i++ {
		lineIdx := (row - i%lineCount + lineCount) % lineCount
		line := m.buffer.Line(lineIdx)

		var idx int
		switch {
		case i == 0:
			// Matches must START before the cursor, but may extend to or
			// beyond it, so the slice keeps len(pattern)-1 extra bytes.
			if col == 0 {
				continue
			}
			limit := min(len(line), col+len(m.searchPattern)-1)
			idx = strings.LastIndex(line[:limit], m.searchPattern)
		case i == lineCount:
			// Full wrap back to the starting line: only matches at or
			// after the cursor remain candidates here.
			idx = strings.LastIndex(line, m.searchPattern)
			if idx < col {
				idx = -1
			}
		default:
			idx = strings.LastIndex(line, m.searchPattern)
		}

		if idx >= 0 {
			m.cursor.Row = lineIdx
			m.cursor.Col = idx
			m.desiredCol = m.cursor.Col
			m.ensureCursorVisible()
			m.statusMessage = "?" + m.searchPattern
			return
		}
	}
	m.statusMessage = "Pattern not found: " + m.searchPattern
}

// repeatSearch steps to the next match in the direction of the original
// search (n) or against it (N), per Vim's n/N semantics.
func repeatSearch(m *editorModel, sameDirection bool) {
	if m.searchPattern == "" {
		return
	}
	if m.searchForward == sameDirection {
		searchNext(m)
	} else {
		searchPrev(m)
	}
}

// registerSearchBindings registers /, ?, n, N bindings.
func registerSearchBindings(m *editorModel) {
	m.registry.Add("/", func(model *editorModel) tea.Cmd {
		model.searchMode = true
		model.searchForward = true
		model.searchBuffer = ""
		return nil
	}, ModeNormal, "Search forward")

	m.registry.Add("?", func(model *editorModel) tea.Cmd {
		model.searchMode = true
		model.searchForward = false
		model.searchBuffer = ""
		return nil
	}, ModeNormal, "Search backward")

	m.registry.Add("n", func(model *editorModel) tea.Cmd {
		repeatSearch(model, true)
		return nil
	}, ModeNormal, "Repeat search")

	m.registry.Add("N", func(model *editorModel) tea.Cmd {
		repeatSearch(model, false)
		return nil
	}, ModeNormal, "Repeat search backward")
}
