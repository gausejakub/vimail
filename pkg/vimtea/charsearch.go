package vimtea

import (
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// handlePendingAction processes the next keypress after a pending action
// like f/F/t/T/r or operator+find combos (df, dt, cf, ct, yf, yt, etc.).
func (m *editorModel) handlePendingAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Cancel on escape
	if key == "esc" {
		m.pendingAction = ""
		return m, nil
	}

	// We need exactly one character
	ch, size := utf8.DecodeRuneInString(key)
	if size == 0 || len(key) != size {
		// Not a single character (e.g. "ctrl+x"), cancel
		m.pendingAction = ""
		return m, nil
	}

	action := m.pendingAction
	m.pendingAction = ""

	switch action {
	case "f":
		executeFindForward(m, byte(ch), false)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "f"
	case "F":
		executeFindBackward(m, byte(ch), false)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "F"
	case "t":
		executeFindForward(m, byte(ch), true)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "t"
	case "T":
		executeFindBackward(m, byte(ch), true)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "T"
	case "r":
		executeReplaceChar(m, byte(ch))
	// Visual mode find
	case "vf":
		executeFindForward(m, byte(ch), false)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "f"
	case "vF":
		executeFindBackward(m, byte(ch), false)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "F"
	case "vt":
		executeFindForward(m, byte(ch), true)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "t"
	case "vT":
		executeFindBackward(m, byte(ch), true)
		m.lastFindChar = byte(ch)
		m.lastFindAction = "T"
	// Operator + find combos
	case "df":
		executeOperatorFind(m, byte(ch), "d", "f")
	case "dF":
		executeOperatorFind(m, byte(ch), "d", "F")
	case "dt":
		executeOperatorFind(m, byte(ch), "d", "t")
	case "dT":
		executeOperatorFind(m, byte(ch), "d", "T")
	case "cf":
		executeOperatorFind(m, byte(ch), "c", "f")
	case "cF":
		executeOperatorFind(m, byte(ch), "c", "F")
	case "ct":
		executeOperatorFind(m, byte(ch), "c", "t")
	case "cT":
		executeOperatorFind(m, byte(ch), "c", "T")
	case "yf":
		executeOperatorFind(m, byte(ch), "y", "f")
	case "yF":
		executeOperatorFind(m, byte(ch), "y", "F")
	case "yt":
		executeOperatorFind(m, byte(ch), "y", "t")
	case "yT":
		executeOperatorFind(m, byte(ch), "y", "T")
	}

	m.keySequence = []string{}
	m.ensureCursorVisible()
	return m, nil
}

// executeFindForward scans right on the current line for ch.
// If till is true, stops one position before the match (t motion).
func executeFindForward(m *editorModel, ch byte, till bool) {
	line := m.buffer.Line(m.cursor.Row)
	for i := m.cursor.Col + 1; i < len(line); i++ {
		if line[i] == ch {
			if till {
				m.cursor.Col = i - 1
			} else {
				m.cursor.Col = i
			}
			m.desiredCol = m.cursor.Col
			return
		}
	}
}

// executeFindBackward scans left on the current line for ch.
// If till is true, stops one position after the match (T motion).
func executeFindBackward(m *editorModel, ch byte, till bool) {
	line := m.buffer.Line(m.cursor.Row)
	for i := m.cursor.Col - 1; i >= 0; i-- {
		if line[i] == ch {
			if till {
				m.cursor.Col = i + 1
			} else {
				m.cursor.Col = i
			}
			m.desiredCol = m.cursor.Col
			return
		}
	}
}

// executeReplaceChar replaces the character at cursor with ch, staying in normal mode.
func executeReplaceChar(m *editorModel, ch byte) {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 || m.cursor.Col >= len(line) {
		return
	}
	m.buffer.saveUndoState(m.cursor)
	newLine := line[:m.cursor.Col] + string(ch) + line[m.cursor.Col+1:]
	m.buffer.setLine(m.cursor.Row, newLine)
}

// executeOperatorFind performs an operator (d/c/y) combined with a find motion (f/F/t/T).
func executeOperatorFind(m *editorModel, ch byte, op, find string) {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 {
		return
	}

	// Save find state for ;/,
	m.lastFindChar = ch
	m.lastFindAction = find

	// Compute target position
	targetCol := -1
	switch find {
	case "f":
		for i := m.cursor.Col + 1; i < len(line); i++ {
			if line[i] == ch {
				targetCol = i
				break
			}
		}
	case "F":
		for i := m.cursor.Col - 1; i >= 0; i-- {
			if line[i] == ch {
				targetCol = i
				break
			}
		}
	case "t":
		for i := m.cursor.Col + 1; i < len(line); i++ {
			if line[i] == ch {
				targetCol = i - 1
				break
			}
		}
	case "T":
		for i := m.cursor.Col - 1; i >= 0; i-- {
			if line[i] == ch {
				targetCol = i + 1
				break
			}
		}
	}

	if targetCol < 0 || targetCol == m.cursor.Col {
		return
	}

	start, end := orderCursors(
		Cursor{Row: m.cursor.Row, Col: m.cursor.Col},
		Cursor{Row: m.cursor.Row, Col: targetCol},
	)

	switch op {
	case "d":
		m.buffer.saveUndoState(m.cursor)
		m.yankBuffer = m.buffer.deleteRange(start, end)
		m.cursor = start
	case "c":
		m.buffer.saveUndoState(m.cursor)
		m.yankBuffer = m.buffer.deleteRange(start, end)
		m.cursor = start
		switchMode(m, ModeInsert)
	case "y":
		m.yankBuffer = m.buffer.getRange(start, end)
		setupYankHighlight(m, start, end, m.yankBuffer, false)
	}
}

// repeatFindForward repeats the last f/F/t/T in the same direction (;).
func repeatFindForward(m *editorModel) tea.Cmd {
	if m.lastFindAction == "" {
		return nil
	}
	switch m.lastFindAction {
	case "f":
		executeFindForward(m, m.lastFindChar, false)
	case "F":
		executeFindBackward(m, m.lastFindChar, false)
	case "t":
		executeFindForward(m, m.lastFindChar, true)
	case "T":
		executeFindBackward(m, m.lastFindChar, true)
	}
	return nil
}

// repeatFindBackward repeats the last f/F/t/T in the opposite direction (,).
func repeatFindBackward(m *editorModel) tea.Cmd {
	if m.lastFindAction == "" {
		return nil
	}
	// Reverse the direction
	switch m.lastFindAction {
	case "f":
		executeFindBackward(m, m.lastFindChar, false)
	case "F":
		executeFindForward(m, m.lastFindChar, false)
	case "t":
		executeFindBackward(m, m.lastFindChar, true)
	case "T":
		executeFindForward(m, m.lastFindChar, true)
	}
	return nil
}

// registerCharSearchBindings registers f/F/t/T/r/;/, bindings.
func registerCharSearchBindings(m *editorModel) {
	// Normal mode
	m.registry.Add("f", func(model *editorModel) tea.Cmd {
		model.pendingAction = "f"
		return nil
	}, ModeNormal, "Find char forward")

	m.registry.Add("F", func(model *editorModel) tea.Cmd {
		model.pendingAction = "F"
		return nil
	}, ModeNormal, "Find char backward")

	m.registry.Add("t", func(model *editorModel) tea.Cmd {
		model.pendingAction = "t"
		return nil
	}, ModeNormal, "Till char forward")

	m.registry.Add("T", func(model *editorModel) tea.Cmd {
		model.pendingAction = "T"
		return nil
	}, ModeNormal, "Till char backward")

	m.registry.Add("r", func(model *editorModel) tea.Cmd {
		model.pendingAction = "r"
		return nil
	}, ModeNormal, "Replace character")

	m.registry.Add(";", repeatFindForward, ModeNormal, "Repeat find forward")
	m.registry.Add(",", repeatFindBackward, ModeNormal, "Repeat find backward")

	// Visual mode
	m.registry.Add("f", func(model *editorModel) tea.Cmd {
		model.pendingAction = "vf"
		return nil
	}, ModeVisual, "Find char forward")

	m.registry.Add("F", func(model *editorModel) tea.Cmd {
		model.pendingAction = "vF"
		return nil
	}, ModeVisual, "Find char backward")

	m.registry.Add("t", func(model *editorModel) tea.Cmd {
		model.pendingAction = "vt"
		return nil
	}, ModeVisual, "Till char forward")

	m.registry.Add("T", func(model *editorModel) tea.Cmd {
		model.pendingAction = "vT"
		return nil
	}, ModeVisual, "Till char backward")

	m.registry.Add(";", repeatFindForward, ModeVisual, "Repeat find forward")
	m.registry.Add(",", repeatFindBackward, ModeVisual, "Repeat find backward")

	// Operator + find combos
	opNames := map[string]string{"d": "Delete", "c": "Change", "y": "Yank"}
	for _, op := range []string{"d", "c", "y"} {
		for _, find := range []string{"f", "F", "t", "T"} {
			binding := op + find
			pendingVal := binding
			m.registry.Add(binding, func(model *editorModel) tea.Cmd {
				model.pendingAction = pendingVal
				return nil
			}, ModeNormal, opNames[op]+" to find char")
		}
	}
}
