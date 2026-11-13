package vimtea

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newReadOnlyEditor(content string) *editorModel {
	e := NewEditor(WithContent(content), WithReadOnly(true), WithEnableStatusBar(false))
	m := e.(*editorModel)
	m.width, m.height = 80, 24
	m.viewport.Width, m.viewport.Height = 80, 24
	return m
}

// collectYanks sends keys and returns the texts of every YankMsg produced.
func collectYanks(m *editorModel, keys ...string) []string {
	var yanks []string
	var walk func(tea.Msg)
	walk = func(msg tea.Msg) {
		switch msg := msg.(type) {
		case YankMsg:
			yanks = append(yanks, msg.Text)
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					walk(c())
				}
			}
		}
	}
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		if cmd != nil {
			walk(cmd())
		}
	}
	return yanks
}

func TestReadOnlyRefusesEdits(t *testing.T) {
	const text = "hello world\nsecond line\nthird"
	m := newReadOnlyEditor(text)
	for _, seq := range [][]string{
		{"x"}, {"d", "d"}, {"D"}, {"d", "w"}, {"c", "w", "X"}, {"i", "X"}, {"a", "X"},
		{"o", "X"}, {"O"}, {"p"}, {"J"}, {"~"}, {"r", "Z"}, {"v", "j", "d"}, {"V", "x"},
		{"u"}, {"ctrl+r"}, {">", ">"},
	} {
		sendKeys(m, seq...)
		sendKeys(m, "esc")
		if got := m.buffer.text(); got != text {
			t.Fatalf("after %v buffer changed to %q", seq, got)
		}
		if m.mode == ModeInsert {
			t.Fatalf("after %v editor entered insert mode", seq)
		}
	}
}

func TestReadOnlyNavigationAndVisualStillWork(t *testing.T) {
	m := newReadOnlyEditor("hello world\nsecond line")
	sendKeys(m, "w")
	assertCursor(t, m, 0, 6)
	sendKeys(m, "j", "0")
	assertCursor(t, m, 1, 0)
	sendKeys(m, "v")
	if m.mode != ModeVisual {
		t.Fatalf("mode = %v, want visual", m.mode)
	}
}

func TestReadOnlyYankEmitsYankMsg(t *testing.T) {
	m := newReadOnlyEditor("hello world\nsecond line")
	if got := collectYanks(m, "y", "i", "w"); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("yiw yanks = %q, want [hello]", got)
	}
	if got := collectYanks(m, "j", "y", "y"); len(got) != 1 || got[0] != "\nsecond line" {
		t.Fatalf("yy yanks = %q, want linewise second line", got)
	}
	// Yanking the same text again is still reported.
	if got := collectYanks(m, "y", "y"); len(got) != 1 {
		t.Fatalf("repeated yy yanks = %q, want one", got)
	}
	if got := collectYanks(m, "k", "0", "v", "e", "y"); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("visual yank = %q, want [hello]", got)
	}
	if got := collectYanks(m, "j", "k", "w"); len(got) != 0 {
		t.Fatalf("motions produced yanks %q", got)
	}
}

func TestEditableEditorDoesNotEmitYankMsg(t *testing.T) {
	m := testEditor("hello")
	if got := collectYanks(m, "y", "y"); len(got) != 0 {
		t.Fatalf("editable editor emitted %q", got)
	}
}

// Control characters delivered as plain runes (pasted text) must not end up
// in the buffer or in a prompt, where they would be drawn raw.
func TestControlCharactersAreNotInserted(t *testing.T) {
	ctl := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

	m := testEditor("abc")
	sendKeys(m, "i")
	for _, r := range []rune{0x1b, 0x1d, 0x07, 0x7f, 'x'} {
		m.Update(ctl(r))
	}
	if got := m.buffer.text(); got != "xabc" {
		t.Fatalf("buffer = %q, want only the printable character inserted", got)
	}

	m = newReadOnlyEditor("abc")
	sendKeys(m, "/")
	for _, r := range []rune{0x1d, 0x1b, 'b'} {
		m.Update(ctl(r))
	}
	if m.searchBuffer != "b" {
		t.Fatalf("search buffer = %q", m.searchBuffer)
	}
}

// A huge numeric prefix must neither overflow, loop for minutes, nor make a
// paste allocate without limit.
func TestHugeCountPrefixIsBounded(t *testing.T) {
	m := testEditor("line one\nline two")
	sendKeys(m, "y", "y")
	for range 30 {
		sendKeys(m, "9")
	}
	if m.countPrefix != maxCountPrefix {
		t.Fatalf("countPrefix = %d, want capped at %d", m.countPrefix, maxCountPrefix)
	}
	sendKeys(m, "p") // would be 10^30 copies
	if n := m.buffer.lineCount(); n < 2 || n > maxCountPrefix+2 {
		t.Fatalf("buffer has %d lines after a capped paste", n)
	}
	for range 30 {
		sendKeys(m, "9")
	}
	sendKeys(m, "j") // must return promptly
	if got := repeatCount(1<<40, 1<<20); got != 8 {
		t.Fatalf("repeatCount = %d, want 8 (8 MiB / 1 MiB)", got)
	}
}

// Screen-relative motions in a pane with no height used to leave the cursor
// on row -1, and the next linewise command indexed the buffer with it.
func TestCursorStaysInsideBufferInZeroHeightPane(t *testing.T) {
	m := testEditor("only line")
	m.SetSize(40, 0)
	for _, keys := range [][]string{{"L", "V", "x"}, {"M", "d", "d"}, {"H", "V", "y"}, {"L", "y", "y"}, {"G", "L", "J"}} {
		sendKeys(m, keys...)
		if m.cursor.Row < 0 || m.cursor.Row >= m.buffer.lineCount() || m.cursor.Col < 0 {
			t.Fatalf("after %v cursor = %+v", keys, m.cursor)
		}
	}
}
