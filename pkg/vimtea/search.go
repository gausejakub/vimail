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

// searchNext finds the next occurrence of the search pattern from cursor.
func searchNext(m *editorModel) {
	if m.searchPattern == "" {
		return
	}

	// Start searching from position after cursor
	row := m.cursor.Row
	col := m.cursor.Col + 1

	for i := 0; i < m.buffer.lineCount(); i++ {
		lineIdx := (row + i) % m.buffer.lineCount()
		line := m.buffer.Line(lineIdx)

		startCol := 0
		if i == 0 {
			startCol = col
		}

		if startCol < len(line) {
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

// searchPrev finds the previous occurrence of the search pattern from cursor.
func searchPrev(m *editorModel) {
	if m.searchPattern == "" {
		return
	}

	row := m.cursor.Row
	col := m.cursor.Col

	for i := 0; i < m.buffer.lineCount(); i++ {
		lineIdx := (row - i + m.buffer.lineCount()) % m.buffer.lineCount()
		line := m.buffer.Line(lineIdx)

		searchIn := line
		if i == 0 {
			if col > 0 {
				searchIn = line[:col]
			} else {
				continue
			}
		}

		idx := strings.LastIndex(searchIn, m.searchPattern)
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
		if model.searchPattern == "" {
			return nil
		}
		searchNext(model)
		return nil
	}, ModeNormal, "Next search result")

	m.registry.Add("N", func(model *editorModel) tea.Cmd {
		if model.searchPattern == "" {
			return nil
		}
		searchPrev(model)
		return nil
	}, ModeNormal, "Previous search result")
}
