package vimtea

import tea "github.com/charmbracelet/bubbletea"

// motionTarget computes the cursor position a motion would land on,
// without actually moving the cursor. It snapshots cursor and viewport
// state, calls the motion, reads the new position, then restores.
func motionTarget(model *editorModel, motion func(*editorModel) tea.Cmd) Cursor {
	saved := model.cursor.Clone()
	savedDesired := model.desiredCol
	savedYOffset := model.viewport.YOffset
	motion(model)
	target := model.cursor.Clone()
	model.cursor = saved
	model.desiredCol = savedDesired
	model.viewport.YOffset = savedYOffset
	return target
}

// orderCursors returns (start, end) so start is always before end.
func orderCursors(a, b Cursor) (Cursor, Cursor) {
	if a.Row < b.Row || (a.Row == b.Row && a.Col <= b.Col) {
		return a, b
	}
	return b, a
}

// deleteMotion deletes from cursor to the position given by motion (inclusive).
func deleteMotion(model *editorModel, motion func(*editorModel) tea.Cmd) tea.Cmd {
	target := motionTarget(model, motion)
	if target.Row == model.cursor.Row && target.Col == model.cursor.Col {
		return nil
	}

	model.buffer.saveUndoState(model.cursor)
	start, end := orderCursors(model.cursor, target)
	model.yankBuffer = model.buffer.deleteRange(start, end)
	model.cursor = start
	model.ensureCursorVisible()
	model.keySequence = []string{}
	return nil
}

// changeMotion deletes from cursor to motion target, then enters insert mode.
func changeMotion(model *editorModel, motion func(*editorModel) tea.Cmd) tea.Cmd {
	target := motionTarget(model, motion)

	model.buffer.saveUndoState(model.cursor)
	start, end := orderCursors(model.cursor, target)
	if start.Row != end.Row || start.Col != end.Col {
		model.yankBuffer = model.buffer.deleteRange(start, end)
	}
	model.cursor = start
	model.ensureCursorVisible()
	model.keySequence = []string{}
	return switchMode(model, ModeInsert)
}

// yankMotion yanks from cursor to motion target.
func yankMotion(model *editorModel, motion func(*editorModel) tea.Cmd) tea.Cmd {
	target := motionTarget(model, motion)
	if target.Row == model.cursor.Row && target.Col == model.cursor.Col {
		return nil
	}

	start, end := orderCursors(model.cursor, target)
	model.yankBuffer = model.buffer.getRange(start, end)
	setupYankHighlight(model, start, end, model.yankBuffer, false)
	model.keySequence = []string{}
	return nil
}

// changeLine deletes the current line content and enters insert mode (cc).
func changeLine(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	line := model.buffer.Line(model.cursor.Row)
	model.yankBuffer = "\n" + line
	model.buffer.setLine(model.cursor.Row, "")
	model.cursor.Col = 0
	model.keySequence = []string{}
	return switchMode(model, ModeInsert)
}

// changeToEndOfLine deletes from cursor to end of line and enters insert mode (C).
func changeToEndOfLine(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	line := model.buffer.Line(model.cursor.Row)
	if model.cursor.Col < len(line) {
		model.yankBuffer = line[model.cursor.Col:]
		model.buffer.setLine(model.cursor.Row, line[:model.cursor.Col])
	}
	return switchMode(model, ModeInsert)
}

// motionEntry defines a motion with its key binding and function.
type motionEntry struct {
	key    string
	motion func(*editorModel) tea.Cmd
	desc   string
}

// registerExtendedBindings adds operator+motion combos and the e motion.
func registerExtendedBindings(m *editorModel) {
	// e motion (normal + visual)
	for _, mode := range []EditorMode{ModeNormal, ModeVisual} {
		m.registry.Add("e", moveToWordEnd, mode, "Move to end of word")
	}

	// All motions available for operator combos
	motions := []motionEntry{
		{"w", moveToNextWordStart, "next word"},
		{"e", moveToWordEnd, "end of word"},
		{"b", moveToPrevWordStart, "previous word"},
		{"$", moveToEndOfLine, "end of line"},
		{"0", moveToStartOfLine, "start of line"},
		{"^", moveToFirstNonWhitespace, "first non-whitespace"},
		{"W", moveToNextWORDStart, "next WORD"},
		{"E", moveToWORDEnd, "end of WORD"},
		{"B", moveToPrevWORDStart, "previous WORD"},
		{"ge", moveToPrevWordEnd, "end of previous word"},
		{"gE", moveToPrevWORDEnd, "end of previous WORD"},
		{"g_", moveToLastNonBlank, "last non-blank"},
		{"gg", moveToStartOfDocument, "document start"},
		{"G", moveToEndOfDocument, "document end"},
		{"%", moveToMatchingBracket, "matching bracket"},
		{"{", moveToPrevParagraph, "previous paragraph"},
		{"}", moveToNextParagraph, "next paragraph"},
	}

	// Register d/c/y + every motion
	for _, mot := range motions {
		motion := mot.motion
		desc := mot.desc

		m.registry.Add("d"+mot.key, func(model *editorModel) tea.Cmd {
			return deleteMotion(model, motion)
		}, ModeNormal, "Delete to "+desc)

		m.registry.Add("c"+mot.key, func(model *editorModel) tea.Cmd {
			return changeMotion(model, motion)
		}, ModeNormal, "Change to "+desc)

		m.registry.Add("y"+mot.key, func(model *editorModel) tea.Cmd {
			return yankMotion(model, motion)
		}, ModeNormal, "Yank to "+desc)
	}

	// Special cases
	m.registry.Add("cc", changeLine, ModeNormal, "Change entire line")
	m.registry.Add("C", changeToEndOfLine, ModeNormal, "Change to end of line")

	// Register all new binding groups
	registerMotionBindings(m)
	registerScrollBindings(m)
	registerSearchBindings(m)
	registerCharSearchBindings(m)
	registerEditingBindings(m)
	registerTextObjectBindings(m)
}
