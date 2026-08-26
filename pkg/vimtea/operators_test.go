package vimtea

import "testing"

// Table-driven regression tests for Vim operator range semantics
// (issue #3): exclusive vs inclusive motion endpoints, forward and
// backward directions, operator-find combos, yank contents, cursor
// placement, and undo.

func TestOperatorMotionRanges(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		keys     []string
		wantText string
		wantYank string
		wantRow  int
		wantCol  int
	}{
		// Forward exclusive: target character not included.
		{"dw deletes word and trailing space", "hello world", []string{"d", "w"}, "world", "hello ", 0, 0},
		{"dw mid-line", "one two three", []string{"w", "d", "w"}, "one three", "two ", 0, 4},
		{"dw on last word clamps to end of line", "hello world", []string{"w", "d", "w"}, "hello ", "world", 0, 5},
		{"dw on only word deletes whole line content", "world", []string{"d", "w"}, "", "world", 0, 0},
		{"dw at last word does not join next line", "foo bar\nbaz", []string{"w", "d", "w"}, "foo \nbaz", "bar", 0, 3},
		{"dW deletes WORD and trailing space", "hello.world foo", []string{"d", "W"}, "foo", "hello.world ", 0, 0},
		{"d} deletes to next paragraph", "line1\nline2\n\nline4", []string{"d", "}"}, "\nline4", "line1\nline2\n", 0, 0},
		// Backward exclusive: range ends just before the starting cursor.
		{"db deletes previous word", "hello world", []string{"w", "d", "b"}, "world", "hello ", 0, 0},
		{"d0 deletes to start of line", "hello world", []string{"$", "d", "0"}, "d", "hello worl", 0, 0},
		// Forward inclusive: target character included.
		{"de deletes to word end", "hello world", []string{"d", "e"}, " world", "hello", 0, 0},
		{"d$ deletes to end of line", "hello world", []string{"l", "l", "d", "$"}, "he", "llo world", 0, 1},
		{"d% deletes through matching bracket", "(hello) world", []string{"d", "%"}, " world", "(hello)", 0, 0},
		// Backward inclusive.
		{"dge deletes back through previous word end", "hello world", []string{"w", "d", "g", "e"}, "hellorld", "o w", 0, 4},
		// Yank: same spans, no buffer change, cursor to span start.
		{"yw yanks word and trailing space", "hello world", []string{"y", "w"}, "hello world", "hello ", 0, 0},
		{"yb yanks backward and moves cursor", "hello world", []string{"w", "y", "b"}, "hello world", "hello ", 0, 0},
		{"ye yanks through word end", "hello world", []string{"y", "e"}, "hello world", "hello", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertText(t, m, tt.wantText)
			assertYank(t, m, tt.wantYank)
			assertCursor(t, m, tt.wantRow, tt.wantCol)
		})
	}
}

func TestOperatorFindRanges(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		keys     []string
		wantText string
		wantYank string
		wantCol  int
	}{
		// f/t are inclusive.
		{"df includes target char", "hello world", []string{"d", "f", "o"}, " world", "hello", 0},
		{"dt stops before target char", "hello world", []string{"d", "t", " "}, " world", "hello", 0},
		{"yf yanks through target char", "hello world", []string{"y", "f", "o"}, "hello world", "hello", 0},
		// F/T are exclusive: the starting cursor character survives.
		{"dF keeps cursor char, includes target", "hello world", []string{"$", "d", "F", "o"}, "hello wd", "orl", 7},
		{"dT keeps cursor char, stops after target", "hello world", []string{"$", "d", "T", "o"}, "hello wod", "rl", 8},
		{"yF yanks backward exclusive", "hello world", []string{"$", "y", "F", "o"}, "hello world", "orl", 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertText(t, m, tt.wantText)
			assertYank(t, m, tt.wantYank)
			assertCursor(t, m, 0, tt.wantCol)
		})
	}
}

func TestChangeWordSpecialCase(t *testing.T) {
	t.Run("cw on word acts like ce", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "c", "w")
		// Only "hello" is removed; the space before "world" survives.
		assertText(t, m, " world")
		assertMode(t, m, ModeInsert)
	})

	t.Run("cw at last char of word changes only that char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "e", "c", "w")
		assertText(t, m, "hell world")
		assertMode(t, m, ModeInsert)
	})

	t.Run("cW on WORD acts like cE", func(t *testing.T) {
		m := testEditor("hello.world foo")
		sendKeys(m, "c", "W")
		assertText(t, m, " foo")
		assertMode(t, m, ModeInsert)
	})

	t.Run("cw on whitespace changes up to next word", func(t *testing.T) {
		m := testEditor("a  bc")
		sendKeys(m, "l", "c", "w")
		// Cursor on first space: the whitespace run is removed.
		assertText(t, m, "abc")
		assertMode(t, m, ModeInsert)
	})

	t.Run("cb changes backward exclusively", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "w", "c", "b")
		assertText(t, m, "world")
		assertCursor(t, m, 0, 0)
		assertMode(t, m, ModeInsert)
	})
}

func TestLinewiseOperators(t *testing.T) {
	t.Run("dG deletes lines to end of document", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "d", "G")
		assertText(t, m, "line1")
		assertYank(t, m, "\nline2\nline3")
		assertCursor(t, m, 0, 0)
	})

	t.Run("dgg deletes lines to start of document", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "d", "g", "g")
		assertText(t, m, "line3")
		assertYank(t, m, "\nline1\nline2")
		assertCursor(t, m, 0, 0)
	})

	t.Run("dG on whole buffer leaves one empty line", func(t *testing.T) {
		m := testEditor("line1\nline2")
		sendKeys(m, "d", "G")
		assertText(t, m, "")
		assertCursor(t, m, 0, 0)
	})

	t.Run("yG yanks linewise and pastes below", func(t *testing.T) {
		m := testEditor("line1\nline2")
		sendKeys(m, "y", "G", "p")
		assertText(t, m, "line1\nline1\nline2\nline2")
	})
}

func TestOperatorUndo(t *testing.T) {
	t.Run("undo after dw restores buffer and cursor", func(t *testing.T) {
		m := testEditor("one two three")
		sendKeys(m, "w", "d", "w")
		assertText(t, m, "one three")
		sendKeys(m, "u")
		assertText(t, m, "one two three")
		assertCursor(t, m, 0, 4)
	})

	t.Run("undo after db restores buffer", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "w", "d", "b", "u")
		assertText(t, m, "hello world")
	})

	t.Run("undo after dF restores buffer", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "d", "F", "o", "u")
		assertText(t, m, "hello world")
	})

	t.Run("change plus typed text is one undo unit", func(t *testing.T) {
		m := testEditor("foo bar")
		sendKeys(m, "c", "w", "b", "a", "z", "esc")
		assertText(t, m, "baz bar")
		sendKeys(m, "u")
		assertText(t, m, "foo bar")
	})

	t.Run("insert session is one undo unit", func(t *testing.T) {
		m := testEditor("x")
		sendKeys(m, "a", "b", "c", "d", "esc")
		assertText(t, m, "xbcd")
		sendKeys(m, "u")
		assertText(t, m, "x")
	})
}

func TestXYanksDeletedChar(t *testing.T) {
	m := testEditor("abc")
	sendKeys(m, "x")
	assertText(t, m, "bc")
	assertYank(t, m, "a")
	sendKeys(m, "p")
	assertText(t, m, "bac")
}
