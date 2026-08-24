package vimtea

import "testing"

// Table-driven regression tests for count prefixes on editing commands
// (issue #8): single- and multi-digit counts, boundary clamping, undo
// coherence, count reset, and no double application through operators.

func TestCountEditingCommands(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		keys     []string
		wantText string
		wantRow  int
		wantCol  int
	}{
		{"2dd deletes two lines", "line1\nline2\nline3", []string{"2", "d", "d"}, "line3", 0, 0},
		{"2dd from middle", "l1\nl2\nl3\nl4", []string{"j", "2", "d", "d"}, "l1\nl4", 1, 0},
		{"5dd clamps to available lines", "l1\nl2", []string{"5", "d", "d"}, "", 0, 0},
		{"3x deletes three characters", "hello world", []string{"3", "x"}, "lo world", 0, 0},
		{"12x deletes with a multi-digit count", "abcdefghijklmno", []string{"1", "2", "x"}, "mno", 0, 0},
		{"9x clamps at end of line", "abc", []string{"l", "9", "x"}, "a", 0, 0},
		{"3~ toggles three characters", "hello", []string{"3", "~"}, "HELlo", 0, 3},
		{"9~ stops at end of line", "ab", []string{"9", "~"}, "AB", 0, 1},
		{"3J joins three lines", "a\nb\nc\nd", []string{"3", "J"}, "a b c\nd", 0, 3},
		{"2J equals J", "a\nb\nc", []string{"2", "J"}, "a b\nc", 0, 1},
		{"2gJ joins without spaces", "a\nb\nc", []string{"3", "g", "J"}, "abc", 0, 2},
		{"2>> indents two lines", "a\nb\nc", []string{"2", ">", ">"}, "\ta\n\tb\nc", 0, 1},
		{"2<< deindents two lines", "\ta\n\tb\nc", []string{"2", "<", "<"}, "a\nb\nc", 0, 0},
		{"3s substitutes three chars", "hello", []string{"3", "s"}, "lo", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertText(t, m, tt.wantText)
			assertCursor(t, m, tt.wantRow, tt.wantCol)
		})
	}
}

func TestCountYankAndPaste(t *testing.T) {
	t.Run("2yy yanks two lines and p pastes both", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "2", "y", "y")
		assertYank(t, m, "\nline1\nline2")
		sendKeys(m, "G", "p")
		assertText(t, m, "line1\nline2\nline3\nline1\nline2")
	})

	t.Run("2dd yanks both deleted lines and p restores them", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "2", "d", "d")
		assertYank(t, m, "\nline1\nline2")
		sendKeys(m, "p")
		assertText(t, m, "line3\nline1\nline2")
	})

	t.Run("3p repeats a charwise paste", func(t *testing.T) {
		m := testEditor("ab")
		sendKeys(m, "x") // yank "a", buffer "b"
		sendKeys(m, "3", "p")
		assertText(t, m, "baaa")
	})

	t.Run("2p repeats a linewise paste", func(t *testing.T) {
		m := testEditor("line1\nline2")
		sendKeys(m, "y", "y", "2", "p")
		assertText(t, m, "line1\nline1\nline1\nline2")
	})
}

func TestCountUndoCoherence(t *testing.T) {
	t.Run("2dd is a single undo operation", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "2", "d", "d")
		assertText(t, m, "line3")
		sendKeys(m, "u")
		assertText(t, m, "line1\nline2\nline3")
	})

	t.Run("3x is a single undo operation", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "3", "x")
		assertText(t, m, "lo")
		sendKeys(m, "u")
		assertText(t, m, "hello")
	})

	t.Run("3J is a single undo operation", func(t *testing.T) {
		m := testEditor("a\nb\nc")
		sendKeys(m, "3", "J", "u")
		assertText(t, m, "a\nb\nc")
	})
}

func TestCountReset(t *testing.T) {
	t.Run("count is consumed by the command it prefixes", func(t *testing.T) {
		m := testEditor("abcdefgh")
		sendKeys(m, "3", "x", "x")
		// 3x then a plain x: 3 + 1 characters deleted, not 3 + 3.
		assertText(t, m, "efgh")
	})

	t.Run("count is dropped on invalid input", func(t *testing.T) {
		m := testEditor("abcdef")
		sendKeys(m, "3", "q", "x")
		// "q" matches nothing, so the pending 3 must not apply to x.
		assertText(t, m, "bcdef")
	})

	t.Run("count is dropped on escape", func(t *testing.T) {
		m := testEditor("abcdef")
		sendKeys(m, "3", "esc", "x")
		assertText(t, m, "bcdef")
	})

	t.Run("count consumed by a motion does not leak", func(t *testing.T) {
		m := testEditor("abcdefgh")
		sendKeys(m, "2", "l", "x")
		assertText(t, m, "abdefgh")
		assertCursor(t, m, 0, 2)
	})
}

func TestCountNotDoubleApplied(t *testing.T) {
	t.Run("2w moves exactly two words", func(t *testing.T) {
		m := testEditor("one two three four")
		sendKeys(m, "2", "w")
		assertCursor(t, m, 0, 8)
	})

	t.Run("2dw deletes exactly two words", func(t *testing.T) {
		m := testEditor("one two three four")
		sendKeys(m, "2", "d", "w")
		// The count is consumed once, by the w motion.
		assertText(t, m, "three four")
	})

	t.Run("2j then dd deletes one line", func(t *testing.T) {
		m := testEditor("a\nb\nc\nd")
		sendKeys(m, "2", "j", "d", "d")
		assertText(t, m, "a\nb\nd")
	})
}
