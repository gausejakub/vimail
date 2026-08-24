package vimtea

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// motionKind classifies how an operator treats a motion's endpoint,
// mirroring Vim's exclusive/inclusive/linewise motion classes
// (:help exclusive).
type motionKind int

const (
	// motionExclusive: the character the motion lands on is NOT part of
	// the operated range (w, b, 0, {, } ...).
	motionExclusive motionKind = iota
	// motionInclusive: the character the motion lands on IS part of the
	// operated range (e, $, %, ge ...).
	motionInclusive
	// motionLinewise: the operation affects whole lines (gg, G).
	motionLinewise
)

// wordMotionType marks w/W operator motions, which get Vim's special
// end-of-line clamping (:help word) on top of exclusive semantics.
type wordMotionType int

const (
	wordMotionNone wordMotionType = iota
	wordMotionSmall
	wordMotionBig
)

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

// isWordStartAt reports whether line[col] begins a new word (small) or
// WORD (big) — i.e. it is a valid stopping point for an operator w/W
// motion on the same line.
func isWordStartAt(line string, col int, wordType wordMotionType) bool {
	if col < 0 || col >= len(line) {
		return false
	}
	if wordType == wordMotionBig {
		return !isWhitespace(line[col]) && (col == 0 || isWhitespace(line[col-1]))
	}
	return !isWordSeparator(line[col]) && (col == 0 || isWordSeparator(line[col-1]))
}

// operatorSpan converts a motion into the [start, end) character span an
// operator should act on. Returns ok=false when the motion goes nowhere.
func operatorSpan(model *editorModel, motion func(*editorModel) tea.Cmd, kind motionKind, wordType wordMotionType) (Cursor, Cursor, bool) {
	cursor := model.cursor.Clone()
	target := motionTarget(model, motion)

	forward := target.Row > cursor.Row || (target.Row == cursor.Row && target.Col > cursor.Col)

	// Vim special case (:help word): when w/W is used after an operator
	// and the motion cannot reach the start of a following word on the
	// current line (last word of the line, or the motion wrapped to the
	// next line), the operation stops at the end of the current line
	// instead of eating into the next line.
	if wordType != wordMotionNone {
		line := model.buffer.Line(cursor.Row)
		if len(line) == 0 || cursor.Col >= len(line) {
			return cursor, cursor, false
		}
		if target.Row != cursor.Row || !forward || !isWordStartAt(line, target.Col, wordType) {
			return cursor, Cursor{Row: cursor.Row, Col: len(line)}, true
		}
		return cursor, target, true
	}

	if target.Row == cursor.Row && target.Col == cursor.Col {
		return cursor, cursor, false
	}

	var start, end Cursor
	if forward {
		start, end = cursor, target
	} else {
		start, end = target, cursor
	}
	if kind == motionInclusive {
		end = model.buffer.advancePos(end)
	}
	return start, end, true
}

// clampCursorCol keeps the cursor on a valid normal-mode column after a
// delete (Vim never leaves the cursor past the last character).
func clampCursorCol(model *editorModel) {
	lineLen := model.buffer.lineLength(model.cursor.Row)
	if model.cursor.Col > max(0, lineLen-1) {
		model.cursor.Col = max(0, lineLen-1)
	}
}

// deleteMotion deletes the character span covered by motion.
func deleteMotion(model *editorModel, motion func(*editorModel) tea.Cmd, kind motionKind, wordType wordMotionType) tea.Cmd {
	if kind == motionLinewise {
		return linewiseOperate(model, motion, "d")
	}
	start, end, ok := operatorSpan(model, motion, kind, wordType)
	if !ok {
		model.keySequence = []string{}
		return nil
	}

	model.buffer.saveUndoState(model.cursor)
	model.yankBuffer = model.buffer.deleteCharRange(start, end)
	model.cursor = start
	clampCursorCol(model)
	model.ensureCursorVisible()
	model.keySequence = []string{}
	return nil
}

// wordEndFromCursor returns the column of the last character of the
// word/WORD under the cursor, used for the cw/cW special case.
func wordEndFromCursor(line string, col int, wordType wordMotionType) int {
	if wordType == wordMotionBig {
		for col+1 < len(line) && !isWhitespace(line[col+1]) {
			col++
		}
		return col
	}
	if isWordSeparator(line[col]) {
		// A punctuation run is a word of its own for the small-w class.
		for col+1 < len(line) && isWordSeparator(line[col+1]) && !isWhitespace(line[col+1]) {
			col++
		}
		return col
	}
	for col+1 < len(line) && !isWordSeparator(line[col+1]) {
		col++
	}
	return col
}

// changeMotion deletes the span covered by motion, then enters insert mode.
func changeMotion(model *editorModel, motion func(*editorModel) tea.Cmd, kind motionKind, wordType wordMotionType) tea.Cmd {
	if kind == motionLinewise {
		return linewiseOperate(model, motion, "c")
	}

	var start, end Cursor
	var ok bool

	line := model.buffer.Line(model.cursor.Row)
	if wordType != wordMotionNone && model.cursor.Col < len(line) && !isWhitespace(line[model.cursor.Col]) {
		// Vim special case (:help cw): on a non-blank, cw behaves like
		// ce — it does not touch the whitespace after the word.
		endCol := wordEndFromCursor(line, model.cursor.Col, wordType)
		start = model.cursor.Clone()
		end = Cursor{Row: start.Row, Col: endCol + 1}
		ok = true
	} else {
		start, end, ok = operatorSpan(model, motion, kind, wordType)
	}

	model.buffer.saveUndoState(model.cursor)
	if ok && (start != end) {
		model.yankBuffer = model.buffer.deleteCharRange(start, end)
		model.cursor = start
	}
	model.ensureCursorVisible()
	model.keySequence = []string{}
	cmd := switchMode(model, ModeInsert)
	model.insertUndoSaved = true
	return cmd
}

// yankMotion yanks the span covered by motion. The cursor moves to the
// start of the yanked text, as in Vim.
func yankMotion(model *editorModel, motion func(*editorModel) tea.Cmd, kind motionKind, wordType wordMotionType) tea.Cmd {
	if kind == motionLinewise {
		return linewiseOperate(model, motion, "y")
	}
	start, end, ok := operatorSpan(model, motion, kind, wordType)
	if !ok {
		model.keySequence = []string{}
		return nil
	}

	text := model.buffer.getCharRange(start, end)
	model.cursor = start
	setupYankHighlight(model, start, inclusiveEndForDisplay(model, end), text, false)
	model.keySequence = []string{}
	return nil
}

// inclusiveEndForDisplay converts an exclusive span end back to the last
// included position, for the yank highlight.
func inclusiveEndForDisplay(model *editorModel, end Cursor) Cursor {
	if end.Col > 0 {
		return Cursor{Row: end.Row, Col: end.Col - 1}
	}
	if end.Row > 0 {
		return Cursor{Row: end.Row - 1, Col: max(0, model.buffer.lineLength(end.Row-1)-1)}
	}
	return end
}

// linewiseOperate implements d/c/y with a linewise motion (gg, G):
// whole lines between the cursor row and the target row are affected.
func linewiseOperate(model *editorModel, motion func(*editorModel) tea.Cmd, op string) tea.Cmd {
	target := motionTarget(model, motion)
	startRow := min(model.cursor.Row, target.Row)
	endRow := max(model.cursor.Row, target.Row)
	endRow = min(endRow, model.buffer.lineCount()-1)

	yanked := "\n" + strings.Join(model.buffer.lines[startRow:endRow+1], "\n")
	model.keySequence = []string{}

	switch op {
	case "y":
		model.cursor = Cursor{Row: startRow, Col: 0}
		setupYankHighlight(model, Cursor{Row: startRow, Col: 0},
			Cursor{Row: endRow, Col: max(0, model.buffer.lineLength(endRow)-1)}, yanked, true)
		return nil
	case "d", "c":
		model.buffer.saveUndoState(model.cursor)
		model.yankBuffer = yanked
		for range endRow - startRow + 1 {
			model.buffer.deleteLine(startRow)
		}
		if op == "c" {
			// cG/cgg leave one empty line open for insertion.
			if model.buffer.lineCount() == 1 && model.buffer.lineLength(0) == 0 && startRow == 0 {
				// deleteLine already left a single empty line
			} else {
				model.buffer.insertLine(min(startRow, model.buffer.lineCount()), "")
			}
			model.cursor = Cursor{Row: min(startRow, model.buffer.lineCount()-1), Col: 0}
			model.ensureCursorVisible()
			cmd := switchMode(model, ModeInsert)
			model.insertUndoSaved = true
			return cmd
		}
		model.cursor = Cursor{Row: min(startRow, model.buffer.lineCount()-1), Col: 0}
		clampCursorCol(model)
		model.ensureCursorVisible()
	}
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
	cmd := switchMode(model, ModeInsert)
	model.insertUndoSaved = true
	return cmd
}

// changeToEndOfLine deletes from cursor to end of line and enters insert mode (C).
func changeToEndOfLine(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	line := model.buffer.Line(model.cursor.Row)
	if model.cursor.Col < len(line) {
		model.yankBuffer = line[model.cursor.Col:]
		model.buffer.setLine(model.cursor.Row, line[:model.cursor.Col])
	}
	cmd := switchMode(model, ModeInsert)
	model.insertUndoSaved = true
	return cmd
}

// motionEntry defines a motion with its key binding, function, and
// operator classification.
type motionEntry struct {
	key      string
	motion   func(*editorModel) tea.Cmd
	desc     string
	kind     motionKind
	wordType wordMotionType
}

// registerExtendedBindings adds operator+motion combos and the e motion.
func registerExtendedBindings(m *editorModel) {
	// e motion (normal + visual)
	for _, mode := range []EditorMode{ModeNormal, ModeVisual} {
		m.registry.Add("e", moveToWordEnd, mode, "Move to end of word")
	}

	// All motions available for operator combos, classified per Vim's
	// motion semantics (:help exclusive, :help inclusive).
	motions := []motionEntry{
		{"w", moveToNextWordStart, "next word", motionExclusive, wordMotionSmall},
		{"e", moveToWordEnd, "end of word", motionInclusive, wordMotionNone},
		{"b", moveToPrevWordStart, "previous word", motionExclusive, wordMotionNone},
		{"$", moveToEndOfLine, "end of line", motionInclusive, wordMotionNone},
		{"0", moveToStartOfLine, "start of line", motionExclusive, wordMotionNone},
		{"^", moveToFirstNonWhitespace, "first non-whitespace", motionExclusive, wordMotionNone},
		{"W", moveToNextWORDStart, "next WORD", motionExclusive, wordMotionBig},
		{"E", moveToWORDEnd, "end of WORD", motionInclusive, wordMotionNone},
		{"B", moveToPrevWORDStart, "previous WORD", motionExclusive, wordMotionNone},
		{"ge", moveToPrevWordEnd, "end of previous word", motionInclusive, wordMotionNone},
		{"gE", moveToPrevWORDEnd, "end of previous WORD", motionInclusive, wordMotionNone},
		{"g_", moveToLastNonBlank, "last non-blank", motionInclusive, wordMotionNone},
		{"gg", moveToStartOfDocument, "document start", motionLinewise, wordMotionNone},
		{"G", moveToEndOfDocument, "document end", motionLinewise, wordMotionNone},
		{"%", moveToMatchingBracket, "matching bracket", motionInclusive, wordMotionNone},
		{"{", moveToPrevParagraph, "previous paragraph", motionExclusive, wordMotionNone},
		{"}", moveToNextParagraph, "next paragraph", motionExclusive, wordMotionNone},
	}

	// Register d/c/y + every motion
	for _, mot := range motions {
		motion := mot.motion
		desc := mot.desc
		kind := mot.kind
		wordType := mot.wordType

		m.registry.Add("d"+mot.key, func(model *editorModel) tea.Cmd {
			return deleteMotion(model, motion, kind, wordType)
		}, ModeNormal, "Delete to "+desc)

		m.registry.Add("c"+mot.key, func(model *editorModel) tea.Cmd {
			return changeMotion(model, motion, kind, wordType)
		}, ModeNormal, "Change to "+desc)

		m.registry.Add("y"+mot.key, func(model *editorModel) tea.Cmd {
			return yankMotion(model, motion, kind, wordType)
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
