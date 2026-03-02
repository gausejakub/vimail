package vimtea

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Test helpers ---

// key constructs a tea.KeyMsg from a string descriptor.
func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "ctrl+y":
		return tea.KeyMsg{Type: tea.KeyCtrlY}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		runes := []rune(s)
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
	}
}

// testEditor creates an *editorModel with content and a sensible viewport size.
func testEditor(content string) *editorModel {
	e := NewEditor(
		WithContent(content),
		WithEnableStatusBar(false),
	)
	m := e.(*editorModel)
	m.width = 80
	m.height = 24
	m.viewport.Width = 80
	m.viewport.Height = 24
	return m
}

// sendKeys sends a sequence of key descriptors through the editor's Update loop.
// It processes any returned commands that produce UndoRedoMsg (needed for undo/redo).
func sendKeys(m *editorModel, keys ...string) {
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		// Process commands that return messages we care about
		if cmd != nil {
			msg := cmd()
			if msg != nil {
				m.Update(msg)
			}
		}
	}
}

// assertText checks that the buffer content matches expected.
func assertText(t *testing.T, m *editorModel, expected string) {
	t.Helper()
	got := m.buffer.text()
	if got != expected {
		t.Errorf("buffer text:\n  got:  %q\n  want: %q", got, expected)
	}
}

// assertCursor checks cursor position.
func assertCursor(t *testing.T, m *editorModel, row, col int) {
	t.Helper()
	if m.cursor.Row != row || m.cursor.Col != col {
		t.Errorf("cursor: got (%d,%d), want (%d,%d)", m.cursor.Row, m.cursor.Col, row, col)
	}
}

// assertMode checks the editor mode.
func assertMode(t *testing.T, m *editorModel, mode EditorMode) {
	t.Helper()
	if m.mode != mode {
		t.Errorf("mode: got %v, want %v", m.mode, mode)
	}
}

// assertYank checks the yank buffer content.
func assertYank(t *testing.T, m *editorModel, expected string) {
	t.Helper()
	if m.yankBuffer != expected {
		t.Errorf("yank buffer:\n  got:  %q\n  want: %q", m.yankBuffer, expected)
	}
}

// --- Scenario tests ---

func TestBasicMotions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		keys    []string
		row     int
		col     int
	}{
		{
			name:    "h moves left",
			content: "hello",
			keys:    []string{"l", "l", "h"},
			row:     0, col: 1,
		},
		{
			name:    "j moves down",
			content: "line1\nline2\nline3",
			keys:    []string{"j", "j"},
			row:     2, col: 0,
		},
		{
			name:    "k moves up",
			content: "line1\nline2\nline3",
			keys:    []string{"j", "j", "k"},
			row:     1, col: 0,
		},
		{
			name:    "l moves right",
			content: "hello",
			keys:    []string{"l", "l"},
			row:     0, col: 2,
		},
		{
			name:    "w moves to next word start",
			content: "hello world foo",
			keys:    []string{"w"},
			row:     0, col: 6,
		},
		{
			name:    "b moves to previous word start",
			content: "hello world foo",
			keys:    []string{"w", "w", "b"},
			row:     0, col: 6,
		},
		{
			name:    "e moves to end of word",
			content: "hello world",
			keys:    []string{"e"},
			row:     0, col: 4,
		},
		{
			name:    "0 moves to start of line",
			content: "hello world",
			keys:    []string{"l", "l", "l", "0"},
			row:     0, col: 0,
		},
		{
			name:    "$ moves to end of line",
			content: "hello world",
			keys:    []string{"$"},
			row:     0, col: 10,
		},
		{
			name:    "^ moves to first non-whitespace",
			content: "   hello",
			keys:    []string{"^"},
			row:     0, col: 3,
		},
		{
			name:    "gg moves to start of document",
			content: "line1\nline2\nline3",
			keys:    []string{"j", "j", "g", "g"},
			row:     0, col: 0,
		},
		{
			name:    "G moves to end of document",
			content: "line1\nline2\nline3",
			keys:    []string{"G"},
			row:     2, col: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertCursor(t, m, tt.row, tt.col)
		})
	}
}

func TestWORDMotions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		keys    []string
		row     int
		col     int
	}{
		{
			name:    "W skips over punctuation",
			content: "hello.world foo",
			keys:    []string{"W"},
			row:     0, col: 12,
		},
		{
			name:    "E skips to end of WORD",
			content: "hello.world foo",
			keys:    []string{"E"},
			row:     0, col: 10,
		},
		{
			name:    "B moves to previous WORD",
			content: "foo bar.baz qux",
			keys:    []string{"$", "B"},
			row:     0, col: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertCursor(t, m, tt.row, tt.col)
		})
	}
}

func TestExtendedMotions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		keys    []string
		row     int
		col     int
	}{
		{
			name:    "ge moves to end of previous word",
			content: "hello world",
			keys:    []string{"w", "g", "e"},
			row:     0, col: 4,
		},
		{
			name:    "gE moves to end of previous WORD",
			content: "hello foo.bar baz",
			keys:    []string{"W", "W", "g", "E"},
			row:     0, col: 12,
		},
		{
			name:    "g_ moves to last non-blank",
			content: "hello   ",
			keys:    []string{"g", "_"},
			row:     0, col: 4,
		},
		{
			name:    "% bounces between matching parens",
			content: "(hello world)",
			keys:    []string{"%"},
			row:     0, col: 12,
		},
		{
			name:    "% bounces back from close paren",
			content: "(hello world)",
			keys:    []string{"%", "%"},
			row:     0, col: 0,
		},
		{
			name:    "} moves to next paragraph",
			content: "line1\nline2\n\nline4\nline5",
			keys:    []string{"}"},
			row:     2, col: 0,
		},
		{
			name:    "{ moves to previous paragraph",
			content: "line1\nline2\n\nline4\nline5",
			keys:    []string{"G", "{"},
			row:     2, col: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertCursor(t, m, tt.row, tt.col)
		})
	}
}

func TestCharSearch(t *testing.T) {
	t.Run("f finds character forward", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "f", "w")
		assertCursor(t, m, 0, 6)
	})

	t.Run("F finds character backward", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "F", "o")
		assertCursor(t, m, 0, 7)
	})

	t.Run("t stops before character", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "t", "w")
		assertCursor(t, m, 0, 5)
	})

	t.Run("T stops after character backward", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "T", "o")
		assertCursor(t, m, 0, 8)
	})

	t.Run("semicolon repeats find forward", func(t *testing.T) {
		m := testEditor("abcabc")
		sendKeys(m, "f", "b", ";")
		assertCursor(t, m, 0, 4)
	})

	t.Run("comma repeats find backward", func(t *testing.T) {
		m := testEditor("abcabc")
		sendKeys(m, "f", "b", ";", ",")
		assertCursor(t, m, 0, 1)
	})

	t.Run("r replaces single character", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "r", "H")
		assertText(t, m, "Hello")
		assertCursor(t, m, 0, 0)
		assertMode(t, m, ModeNormal)
	})
}

func TestScreenMotions(t *testing.T) {
	content := "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9"

	t.Run("H moves to screen top", func(t *testing.T) {
		m := testEditor(content)
		sendKeys(m, "j", "j", "j", "H")
		assertCursor(t, m, 0, 0)
	})

	t.Run("M moves to screen middle", func(t *testing.T) {
		m := testEditor(content)
		sendKeys(m, "M")
		// height=24, so middle is line 12. But we only have 10 lines, so it's clamped.
		if m.cursor.Row != 9 && m.cursor.Row != 12 {
			// Accept either: clamped to last line, or actual middle
			assertCursor(t, m, min(24/2, 9), 0)
		}
	})

	t.Run("L moves to screen bottom", func(t *testing.T) {
		m := testEditor(content)
		sendKeys(m, "L")
		// Last line visible or last line of document
		if m.cursor.Row != 9 && m.cursor.Row != 23 {
			assertCursor(t, m, min(23, 9), 0)
		}
	})
}

func TestDeleteOperator(t *testing.T) {
	t.Run("dw deletes to next word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "w")
		assertText(t, m, "world")
	})

	t.Run("de deletes to end of word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "e")
		assertText(t, m, " world")
	})

	t.Run("db deletes to previous word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "w", "d", "b")
		assertText(t, m, "world")
	})

	t.Run("d$ deletes to end of line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "l", "l", "d", "$")
		assertText(t, m, "he")
	})

	t.Run("d0 deletes to start of line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "d", "0")
		assertText(t, m, "d")
	})

	t.Run("dd deletes current line", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "d", "d")
		assertText(t, m, "line1\nline3")
		assertCursor(t, m, 1, 0)
	})

	t.Run("D deletes to end of line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "l", "l", "D")
		assertText(t, m, "he")
	})

	t.Run("x deletes character at cursor", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "x")
		assertText(t, m, "ello")
	})

	t.Run("dW deletes to next WORD", func(t *testing.T) {
		m := testEditor("hello.world foo")
		sendKeys(m, "d", "W")
		assertText(t, m, "foo")
	})

	t.Run("d% deletes to matching bracket", func(t *testing.T) {
		m := testEditor("(hello) world")
		sendKeys(m, "d", "%")
		assertText(t, m, " world")
	})

	t.Run("d} deletes to next paragraph", func(t *testing.T) {
		m := testEditor("line1\nline2\n\nline4")
		sendKeys(m, "d", "}")
		assertText(t, m, "\nline4")
	})
}

func TestChangeOperator(t *testing.T) {
	t.Run("cw changes to next word and enters insert", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "c", "w")
		assertMode(t, m, ModeInsert)
		// Now type replacement
		sendKeys(m, "g", "o", "o", "d", " ", "esc")
		assertText(t, m, "good world")
		assertMode(t, m, ModeNormal)
	})

	t.Run("cc changes entire line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "c", "c")
		assertMode(t, m, ModeInsert)
		assertText(t, m, "")
		sendKeys(m, "n", "e", "w", "esc")
		assertText(t, m, "new")
	})

	t.Run("C changes to end of line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "l", "l", "C")
		assertMode(t, m, ModeInsert)
		assertText(t, m, "he")
		sendKeys(m, "l", "p", "esc")
		assertText(t, m, "help")
	})

	t.Run("s substitutes character", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "s")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "H", "esc")
		assertText(t, m, "Hello")
	})

	t.Run("S substitutes line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "S")
		assertMode(t, m, ModeInsert)
		assertText(t, m, "")
	})
}

func TestYankAndPaste(t *testing.T) {
	t.Run("yw yanks word then p pastes", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "y", "w")
		assertMode(t, m, ModeNormal)
		sendKeys(m, "$", "p")
		assertText(t, m, "hello worldhello ")
	})

	t.Run("yy yanks line then p pastes below", func(t *testing.T) {
		m := testEditor("line1\nline2")
		sendKeys(m, "y", "y", "p")
		assertText(t, m, "line1\nline1\nline2")
	})

	t.Run("dd followed by p puts deleted line back", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "d", "d", "p")
		assertText(t, m, "line1\nline2\nline3")
	})
}

func TestTextObjects(t *testing.T) {
	t.Run("diw deletes inner word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "i", "w")
		assertText(t, m, " world")
	})

	t.Run("daw deletes around word including whitespace", func(t *testing.T) {
		m := testEditor("hello world foo")
		sendKeys(m, "w", "d", "a", "w")
		assertText(t, m, "hello foo")
	})

	t.Run("ciw changes inner word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "c", "i", "w")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "g", "o", "o", "d", "esc")
		assertText(t, m, "good world")
	})

	t.Run("yiw yanks inner word", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "y", "i", "w")
		assertYank(t, m, "hello")
	})

	t.Run("diW deletes inner WORD", func(t *testing.T) {
		m := testEditor("hello.world foo")
		sendKeys(m, "d", "i", "W")
		assertText(t, m, " foo")
	})

	t.Run("dib deletes inner parens", func(t *testing.T) {
		m := testEditor("call(arg1, arg2)")
		sendKeys(m, "f", "(", "d", "i", "b")
		assertText(t, m, "call()")
	})

	t.Run("dab deletes around parens", func(t *testing.T) {
		m := testEditor("call(arg1, arg2) end")
		sendKeys(m, "f", "(", "d", "a", "b")
		assertText(t, m, "call end")
	})

	t.Run("diB deletes inner braces", func(t *testing.T) {
		m := testEditor("func{body}")
		sendKeys(m, "f", "{", "d", "i", "B")
		assertText(t, m, "func{}")
	})

	t.Run("daB deletes around braces", func(t *testing.T) {
		m := testEditor("func{body} end")
		sendKeys(m, "f", "{", "d", "a", "B")
		assertText(t, m, "func end")
	})

	t.Run("ci( changes inner parens", func(t *testing.T) {
		m := testEditor("fn(old)")
		sendKeys(m, "f", "(", "c", "i", "(")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "n", "e", "w", "esc")
		assertText(t, m, "fn(new)")
	})

	t.Run("di[ deletes inner brackets", func(t *testing.T) {
		m := testEditor("arr[index]")
		sendKeys(m, "f", "[", "d", "i", "[")
		assertText(t, m, "arr[]")
	})
}

func TestOperatorFind(t *testing.T) {
	t.Run("df deletes to and including char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "f", "o")
		assertText(t, m, " world")
	})

	t.Run("dt deletes to before char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "t", " ")
		assertText(t, m, " world")
	})

	t.Run("cf changes to and including char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "c", "f", "o")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "X", "esc")
		assertText(t, m, "X world")
	})

	t.Run("ct changes to before char", func(t *testing.T) {
		m := testEditor("func(args)")
		sendKeys(m, "c", "t", "(")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "m", "e", "t", "h", "o", "d", "esc")
		assertText(t, m, "method(args)")
	})

	t.Run("yf yanks to and including char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "y", "f", "o")
		assertYank(t, m, "hello")
	})

	t.Run("dF deletes backward to char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "d", "F", "o")
		assertText(t, m, "hello wd")
	})

	t.Run("dT deletes backward to after char", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "$", "d", "T", "o")
		assertText(t, m, "hello wod")
	})
}

func TestEditing(t *testing.T) {
	t.Run("J joins lines with space", func(t *testing.T) {
		m := testEditor("hello\nworld")
		sendKeys(m, "J")
		assertText(t, m, "hello world")
		assertCursor(t, m, 0, 5)
	})

	t.Run("gJ joins lines without space", func(t *testing.T) {
		m := testEditor("hello\nworld")
		sendKeys(m, "g", "J")
		assertText(t, m, "helloworld")
		assertCursor(t, m, 0, 5)
	})

	t.Run("J trims leading whitespace from second line", func(t *testing.T) {
		m := testEditor("hello\n   world")
		sendKeys(m, "J")
		assertText(t, m, "hello world")
	})

	t.Run("~ toggles case and advances", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "~", "~", "~")
		assertText(t, m, "HELlo")
		assertCursor(t, m, 0, 3)
	})

	t.Run(">> indents line", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, ">", ">")
		assertText(t, m, "\thello")
	})

	t.Run("<< deindents line", func(t *testing.T) {
		m := testEditor("\thello")
		sendKeys(m, "<", "<")
		assertText(t, m, "hello")
	})

	t.Run("<< deindents spaces", func(t *testing.T) {
		m := testEditor("    hello")
		sendKeys(m, "<", "<")
		assertText(t, m, "hello")
	})
}

func TestSearch(t *testing.T) {
	t.Run("/ followed by pattern and enter finds forward", func(t *testing.T) {
		m := testEditor("hello world hello")
		sendKeys(m, "/", "w", "o", "r", "l", "d", "enter")
		assertCursor(t, m, 0, 6)
	})

	t.Run("n finds next occurrence", func(t *testing.T) {
		m := testEditor("abc abc abc")
		sendKeys(m, "/", "a", "b", "c", "enter")
		// First search lands on col 4 (second abc)
		assertCursor(t, m, 0, 4)
		sendKeys(m, "n")
		assertCursor(t, m, 0, 8)
	})

	t.Run("N finds previous occurrence", func(t *testing.T) {
		m := testEditor("abc abc abc")
		sendKeys(m, "$", "/", "a", "b", "c", "enter")
		// Wraps around to col 0
		assertCursor(t, m, 0, 0)
		sendKeys(m, "n")
		assertCursor(t, m, 0, 4)
		sendKeys(m, "N")
		assertCursor(t, m, 0, 0)
	})

	t.Run("? searches backward", func(t *testing.T) {
		m := testEditor("abc def abc")
		sendKeys(m, "$", "?", "a", "b", "c", "enter")
		assertCursor(t, m, 0, 8)
	})

	t.Run("search across lines", func(t *testing.T) {
		m := testEditor("hello\nworld\nhello")
		sendKeys(m, "/", "w", "o", "r", "l", "d", "enter")
		assertCursor(t, m, 1, 0)
	})

	t.Run("esc cancels search", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "/", "w", "o", "esc")
		assertCursor(t, m, 0, 0) // should not have moved
		assertMode(t, m, ModeNormal)
	})
}

func TestVisualMode(t *testing.T) {
	t.Run("v selects and d deletes", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "v", "l", "l", "l", "l", "d")
		assertText(t, m, " world")
		assertMode(t, m, ModeNormal)
	})

	t.Run("v selects and y yanks", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "v", "l", "l", "l", "l", "y")
		assertYank(t, m, "hello")
		assertMode(t, m, ModeNormal)
	})

	t.Run("V selects line and d deletes", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "V", "d")
		assertText(t, m, "line1\nline3")
		assertMode(t, m, ModeNormal)
	})

	t.Run("visual ~ toggles case", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "v", "l", "l", "l", "l", "~")
		assertText(t, m, "HELLO")
		assertMode(t, m, ModeNormal)
	})

	t.Run("visual u lowercases", func(t *testing.T) {
		m := testEditor("HELLO")
		sendKeys(m, "v", "l", "l", "l", "l", "u")
		assertText(t, m, "hello")
	})

	t.Run("visual U uppercases", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "v", "l", "l", "l", "l", "U")
		assertText(t, m, "HELLO")
	})

	t.Run("visual > indents selection", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "V", "j", ">")
		assertText(t, m, "\tline1\n\tline2\nline3")
	})

	t.Run("visual < deindents selection", func(t *testing.T) {
		m := testEditor("\tline1\n\tline2\nline3")
		sendKeys(m, "V", "j", "<")
		assertText(t, m, "line1\nline2\nline3")
	})

	t.Run("visual o swaps selection end", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "v", "l", "l", "l")
		// cursor at col 3, visualStart at col 0
		assertCursor(t, m, 0, 3)
		sendKeys(m, "o")
		// now cursor should be at col 0, visualStart at col 3
		assertCursor(t, m, 0, 0)
	})

	t.Run("esc exits visual mode", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "v", "l", "esc")
		assertMode(t, m, ModeNormal)
	})
}

func TestInsertMode(t *testing.T) {
	t.Run("i enters insert mode at cursor", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "i")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "X", "esc")
		assertText(t, m, "Xhello")
	})

	t.Run("a appends after cursor", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "a")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "X", "esc")
		assertText(t, m, "hXello")
	})

	t.Run("A appends at end of line", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "A")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "!", "esc")
		assertText(t, m, "hello!")
	})

	t.Run("I inserts at start of line", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "l", "l", "I")
		assertMode(t, m, ModeInsert)
		sendKeys(m, ">", "esc")
		assertText(t, m, ">hello")
	})

	t.Run("o opens line below", func(t *testing.T) {
		m := testEditor("hello\nworld")
		sendKeys(m, "o")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "m", "i", "d", "esc")
		assertText(t, m, "hello\nmid\nworld")
	})

	t.Run("O opens line above", func(t *testing.T) {
		m := testEditor("hello\nworld")
		sendKeys(m, "j", "O")
		assertMode(t, m, ModeInsert)
		sendKeys(m, "m", "i", "d", "esc")
		assertText(t, m, "hello\nmid\nworld")
	})

	t.Run("backspace deletes backward", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "A")
		sendKeys(m, "backspace", "backspace", "esc")
		assertText(t, m, "hel")
	})

	t.Run("enter splits line", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "l", "l", "l", "l", "l", "i")
		sendKeys(m, "enter", "esc")
		assertText(t, m, "hello\n world")
	})

	t.Run("ctrl+w deletes word backward in insert", func(t *testing.T) {
		m := testEditor("")
		sendKeys(m, "i", "h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d")
		sendKeys(m, "ctrl+w")
		assertText(t, m, "hello ")
		sendKeys(m, "esc")
	})
}

func TestUndoRedo(t *testing.T) {
	t.Run("u undoes last change", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "x") // delete 'h' → "ello"
		assertText(t, m, "ello")
		sendKeys(m, "u")
		assertText(t, m, "hello")
	})

	t.Run("ctrl+r redoes", func(t *testing.T) {
		m := testEditor("hello")
		sendKeys(m, "x")
		assertText(t, m, "ello")
		sendKeys(m, "u")
		assertText(t, m, "hello")
		sendKeys(m, "ctrl+r")
		assertText(t, m, "ello")
	})

	t.Run("multiple undos", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "d", "w") // → "world"
		sendKeys(m, "d", "w") // → ""
		sendKeys(m, "u")
		assertText(t, m, "world")
		sendKeys(m, "u")
		assertText(t, m, "hello world")
	})
}

func TestScrolling(t *testing.T) {
	// Create enough lines to need scrolling
	lines := ""
	for i := 0; i < 50; i++ {
		if i > 0 {
			lines += "\n"
		}
		lines += "line"
	}

	t.Run("ctrl+d scrolls down half page", func(t *testing.T) {
		m := testEditor(lines)
		sendKeys(m, "ctrl+d")
		// Should move cursor down by height/2 = 12
		if m.cursor.Row != 12 {
			t.Errorf("cursor row: got %d, want 12", m.cursor.Row)
		}
	})

	t.Run("ctrl+u scrolls up half page", func(t *testing.T) {
		m := testEditor(lines)
		sendKeys(m, "ctrl+d", "ctrl+d", "ctrl+u")
		// Down 12, down 12 = 24, up 12 = 12
		if m.cursor.Row != 12 {
			t.Errorf("cursor row: got %d, want 12", m.cursor.Row)
		}
	})

	t.Run("ctrl+f scrolls down full page", func(t *testing.T) {
		m := testEditor(lines)
		sendKeys(m, "ctrl+f")
		// height-2 = 22
		if m.cursor.Row != 22 {
			t.Errorf("cursor row: got %d, want 22", m.cursor.Row)
		}
	})

	t.Run("ctrl+b scrolls up full page", func(t *testing.T) {
		m := testEditor(lines)
		sendKeys(m, "ctrl+f", "ctrl+b")
		if m.cursor.Row != 0 {
			t.Errorf("cursor row: got %d, want 0", m.cursor.Row)
		}
	})

	t.Run("zz centers cursor", func(t *testing.T) {
		m := testEditor(lines)
		// Move to line 25
		for i := 0; i < 25; i++ {
			sendKeys(m, "j")
		}
		sendKeys(m, "z", "z")
		// viewport should be centered on line 25
		expected := 25 - 12 // row - height/2
		if m.viewport.YOffset != expected {
			t.Errorf("viewport YOffset: got %d, want %d", m.viewport.YOffset, expected)
		}
	})

	t.Run("zt puts cursor at top", func(t *testing.T) {
		m := testEditor(lines)
		for i := 0; i < 25; i++ {
			sendKeys(m, "j")
		}
		sendKeys(m, "z", "t")
		if m.viewport.YOffset != 25 {
			t.Errorf("viewport YOffset: got %d, want 25", m.viewport.YOffset)
		}
	})

	t.Run("zb puts cursor at bottom", func(t *testing.T) {
		m := testEditor(lines)
		for i := 0; i < 25; i++ {
			sendKeys(m, "j")
		}
		sendKeys(m, "z", "b")
		// YOffset = cursor - height + 1 = 25 - 24 + 1 = 2
		expected := 25 - 24 + 1
		if m.viewport.YOffset != expected {
			t.Errorf("viewport YOffset: got %d, want %d", m.viewport.YOffset, expected)
		}
	})
}

func TestCountPrefix(t *testing.T) {
	t.Run("3j moves down 3 lines", func(t *testing.T) {
		m := testEditor("a\nb\nc\nd\ne")
		sendKeys(m, "3", "j")
		assertCursor(t, m, 3, 0)
	})

	t.Run("2dd deletes 2 lines", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "2", "d", "d")
		// 2dd should delete the current line twice
		assertText(t, m, "line3")
	})

	t.Run("5l moves right 5", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "5", "l")
		assertCursor(t, m, 0, 5)
	})

	t.Run("3x deletes 3 characters", func(t *testing.T) {
		m := testEditor("hello world")
		sendKeys(m, "3", "x")
		assertText(t, m, "lo world")
	})
}

// --- Complex scenario tests ---

func TestScenarioWriteEmail(t *testing.T) {
	// Simulate writing a short email in the compose editor
	m := testEditor("")

	// Type greeting
	sendKeys(m, "i")
	for _, ch := range "Hello," {
		sendKeys(m, string(ch))
	}
	sendKeys(m, "enter", "enter")

	// Type body
	for _, ch := range "Thanks for your message." {
		sendKeys(m, string(ch))
	}
	sendKeys(m, "enter")
	for _, ch := range "I will review it soon." {
		sendKeys(m, string(ch))
	}

	sendKeys(m, "esc")
	assertMode(t, m, ModeNormal)
	assertText(t, m, "Hello,\n\nThanks for your message.\nI will review it soon.")
}

func TestScenarioFixTypo(t *testing.T) {
	m := testEditor("Teh quick brown fox")

	// Navigate to 'e' in "Teh", delete it, put it after 'h'
	// 1. Go to 'e' (col 1)
	sendKeys(m, "l")
	// 2. Delete the 'e'
	sendKeys(m, "x")
	assertText(t, m, "Th quick brown fox")
	// 3. Paste after 'h' (cursor is now on 'h' at col 1)
	sendKeys(m, "p")
	assertText(t, m, "The quick brown fox")
}

func TestScenarioSwapWords(t *testing.T) {
	m := testEditor("world hello")

	// Delete first word, go to end, paste
	sendKeys(m, "d", "w")   // → "hello"
	assertText(t, m, "hello")
	sendKeys(m, "A")         // append at end
	sendKeys(m, " ", "esc")  // add space
	sendKeys(m, "p")         // paste "world " after cursor
	assertText(t, m, "hello world ")
}

func TestScenarioDeleteInsideBrackets(t *testing.T) {
	m := testEditor("function(oldArg1, oldArg2)")

	// Navigate inside parens and change the contents
	sendKeys(m, "f", "(", "c", "i", "b")
	assertMode(t, m, ModeInsert)
	for _, ch := range "newArg" {
		sendKeys(m, string(ch))
	}
	sendKeys(m, "esc")
	assertText(t, m, "function(newArg)")
}

func TestScenarioSearchAndReplace(t *testing.T) {
	m := testEditor("foo bar foo baz foo")

	// Search for "foo"
	sendKeys(m, "/", "f", "o", "o", "enter")
	// First match: col 8 (second foo)
	assertCursor(t, m, 0, 8)

	// Change this occurrence
	sendKeys(m, "c", "i", "w")
	for _, ch := range "qux" {
		sendKeys(m, string(ch))
	}
	sendKeys(m, "esc")
	assertText(t, m, "foo bar qux baz foo")
}

func TestScenarioIndentBlock(t *testing.T) {
	m := testEditor("if true {\ndo_something()\ndo_other()\n}")

	// Select the body lines and indent them
	sendKeys(m, "j") // go to "do_something()"
	sendKeys(m, "V", "j", ">")
	assertText(t, m, "if true {\n\tdo_something()\n\tdo_other()\n}")
}

func TestScenarioToggleCaseBlock(t *testing.T) {
	m := testEditor("hello world")

	// Select all and uppercase
	sendKeys(m, "v", "$", "U")
	assertText(t, m, "HELLO WORLD")
}

func TestScenarioJoinMultipleLines(t *testing.T) {
	m := testEditor("line1\n  line2\n  line3")

	sendKeys(m, "J")
	assertText(t, m, "line1 line2\n  line3")
	sendKeys(m, "J")
	assertText(t, m, "line1 line2 line3")
}

func TestScenarioDeleteToMatchingBrace(t *testing.T) {
	m := testEditor("before {inside} after")

	sendKeys(m, "f", "{", "d", "%")
	assertText(t, m, "before  after")
}

func TestScenarioMultiLineYankPaste(t *testing.T) {
	m := testEditor("line1\nline2\nline3\nline4")

	// Yank line 2, paste it after line 4
	sendKeys(m, "j", "y", "y") // yank "line2"
	sendKeys(m, "G")            // go to last line
	sendKeys(m, "p")            // paste after
	assertText(t, m, "line1\nline2\nline3\nline4\nline2")
}

func TestScenarioReplaceMultipleChars(t *testing.T) {
	m := testEditor("aXbXc")

	// Replace all X's with -
	sendKeys(m, "f", "X", "r", "-")
	assertText(t, m, "a-bXc")
	sendKeys(m, ";", "r", "-")
	assertText(t, m, "a-b-c")
}

func TestScenarioUndoComplexEdits(t *testing.T) {
	m := testEditor("hello world foo bar")

	// Delete word
	sendKeys(m, "d", "w")
	assertText(t, m, "world foo bar")

	// Delete another word
	sendKeys(m, "d", "w")
	assertText(t, m, "foo bar")

	// Change word
	sendKeys(m, "c", "w")
	for _, ch := range "baz " {
		sendKeys(m, string(ch))
	}
	sendKeys(m, "esc")
	assertText(t, m, "baz bar")

	// Undo change
	sendKeys(m, "u")
	assertText(t, m, "foo bar")

	// Undo second delete
	sendKeys(m, "u")
	assertText(t, m, "world foo bar")

	// Undo first delete
	sendKeys(m, "u")
	assertText(t, m, "hello world foo bar")

	// Redo
	sendKeys(m, "ctrl+r")
	assertText(t, m, "world foo bar")
}

func TestScenarioParagraphNavigation(t *testing.T) {
	content := "first paragraph\nstill first\n\nsecond paragraph\nstill second\n\nthird"
	m := testEditor(content)

	sendKeys(m, "}")
	assertCursor(t, m, 2, 0) // blank line after first paragraph

	sendKeys(m, "}")
	assertCursor(t, m, 5, 0) // blank line after second paragraph

	sendKeys(m, "{")
	assertCursor(t, m, 2, 0) // back to first blank line
}
