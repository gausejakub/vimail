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
		executeFindForward(m, ch, false)
		m.lastFindChar = ch
		m.lastFindAction = "f"
	case "F":
		executeFindBackward(m, ch, false)
		m.lastFindChar = ch
		m.lastFindAction = "F"
	case "t":
		executeFindForward(m, ch, true)
		m.lastFindChar = ch
		m.lastFindAction = "t"
	case "T":
		executeFindBackward(m, ch, true)
		m.lastFindChar = ch
		m.lastFindAction = "T"
	case "r":
		executeReplaceChar(m, ch)
	// Visual mode find
	case "vf":
		executeFindForward(m, ch, false)
		m.lastFindChar = ch
		m.lastFindAction = "f"
	case "vF":
		executeFindBackward(m, ch, false)
		m.lastFindChar = ch
		m.lastFindAction = "F"
	case "vt":
		executeFindForward(m, ch, true)
		m.lastFindChar = ch
		m.lastFindAction = "t"
	case "vT":
		executeFindBackward(m, ch, true)
		m.lastFindChar = ch
		m.lastFindAction = "T"
	// Operator + find combos
	case "df":
		executeOperatorFind(m, ch, "d", "f")
	case "dF":
		executeOperatorFind(m, ch, "d", "F")
	case "dt":
		executeOperatorFind(m, ch, "d", "t")
	case "dT":
		executeOperatorFind(m, ch, "d", "T")
	case "cf":
		executeOperatorFind(m, ch, "c", "f")
	case "cF":
		executeOperatorFind(m, ch, "c", "F")
	case "ct":
		executeOperatorFind(m, ch, "c", "t")
	case "cT":
		executeOperatorFind(m, ch, "c", "T")
	case "yf":
		executeOperatorFind(m, ch, "y", "f")
	case "yF":
		executeOperatorFind(m, ch, "y", "F")
	case "yt":
		executeOperatorFind(m, ch, "y", "t")
	case "yT":
		executeOperatorFind(m, ch, "y", "T")
	}

	m.keySequence = []string{}
	m.ensureCursorVisible()
	return m, nil
}

// executeFindForward scans right on the current line for ch.
// If till is true, stops one position before the match (t motion).
func executeFindForward(m *editorModel, ch rune, till bool) {
	line := m.buffer.Line(m.cursor.Row)
	i := findRuneForward(line, runeEnd(line, m.cursor.Col), ch)
	if i < 0 {
		return
	}
	if till {
		i = prevRuneStart(line, i)
	}
	m.cursor.Col = i
	m.desiredCol = m.cursor.Col
}

// executeFindBackward scans left on the current line for ch.
// If till is true, stops one position after the match (T motion).
func executeFindBackward(m *editorModel, ch rune, till bool) {
	line := m.buffer.Line(m.cursor.Row)
	i := findRuneBackward(line, m.cursor.Col, ch)
	if i < 0 {
		return
	}
	if till {
		i = runeEnd(line, i)
	}
	m.cursor.Col = i
	m.desiredCol = m.cursor.Col
}

// executeReplaceChar replaces the character at cursor with ch, staying in normal mode.
func executeReplaceChar(m *editorModel, ch rune) {
	line := m.buffer.Line(m.cursor.Row)
	if len(line) == 0 || m.cursor.Col >= len(line) {
		return
	}
	m.buffer.saveUndoState(m.cursor)
	col := runeStart(line, m.cursor.Col)
	m.buffer.setLine(m.cursor.Row, line[:col]+string(ch)+line[runeEnd(line, col):])
}

// executeOperatorFind performs an operator (d/c/y) combined with a find motion (f/F/t/T).
func executeOperatorFind(m *editorModel, ch rune, op, find string) {
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
		targetCol = findRuneForward(line, runeEnd(line, m.cursor.Col), ch)
	case "F":
		targetCol = findRuneBackward(line, m.cursor.Col, ch)
	case "t":
		if i := findRuneForward(line, runeEnd(line, m.cursor.Col), ch); i >= 0 {
			targetCol = prevRuneStart(line, i)
		}
	case "T":
		if i := findRuneBackward(line, m.cursor.Col, ch); i >= 0 {
			targetCol = runeEnd(line, i)
		}
	}

	if targetCol < 0 || targetCol == m.cursor.Col {
		return
	}

	// Vim motion classes: f and t are inclusive (the target character is
	// part of the operated range), F and T are exclusive (the range ends
	// just before the starting cursor position). Spans below use the
	// exclusive-end [start, end) convention of deleteCharRange.
	var start, end Cursor
	switch find {
	case "f", "t":
		start = Cursor{Row: m.cursor.Row, Col: m.cursor.Col}
		end = Cursor{Row: m.cursor.Row, Col: runeEnd(line, targetCol)}
	case "F", "T":
		start = Cursor{Row: m.cursor.Row, Col: targetCol}
		end = Cursor{Row: m.cursor.Row, Col: m.cursor.Col}
	}

	switch op {
	case "d":
		m.buffer.saveUndoState(m.cursor)
		m.yankBuffer = m.buffer.deleteCharRange(start, end)
		m.cursor = start
	case "c":
		m.buffer.saveUndoState(m.cursor)
		m.yankBuffer = m.buffer.deleteCharRange(start, end)
		m.cursor = start
		switchMode(m, ModeInsert)
		m.insertUndoSaved = true
	case "y":
		text := m.buffer.getCharRange(start, end)
		m.cursor = start
		setupYankHighlight(m, start, Cursor{Row: end.Row, Col: prevRuneStart(line, end.Col)}, text, false)
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
