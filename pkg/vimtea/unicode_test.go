package vimtea

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// typeText sends each character of s as its own key press, as a terminal does.
func typeText(m *editorModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestTypingNonASCIICharacters(t *testing.T) {
	m := testEditor("")
	sendKeys(m, "i")
	typeText(m, "Příliš žluťoučký kůň úpěl ďábelské ódy — 日本語 👍")
	sendKeys(m, "esc")
	assertText(t, m, "Příliš žluťoučký kůň úpěl ďábelské ódy — 日本語 👍")
}

func TestBackspaceRemovesWholeCharacter(t *testing.T) {
	m := testEditor("")
	sendKeys(m, "i")
	typeText(m, "čau")
	sendKeys(m, "backspace", "backspace", "backspace")
	assertText(t, m, "")
	typeText(m, "ž")
	sendKeys(m, "esc")
	assertText(t, m, "ž")
}

func TestMovingAndEditingOverAccentedText(t *testing.T) {
	m := testEditor("čšř")
	sendKeys(m, "l")
	assertCursor(t, m, 0, 2) // on "š": characters are two bytes each
	sendKeys(m, "l", "l", "l")
	assertCursor(t, m, 0, 4) // stays on the last character "ř"
	sendKeys(m, "h")
	assertCursor(t, m, 0, 2)

	sendKeys(m, "x") // deletes the whole "š"
	assertText(t, m, "čř")

	sendKeys(m, "0", "a")
	typeText(m, "é") // appended after "č", not inside it
	sendKeys(m, "esc")
	assertText(t, m, "čéř")

	sendKeys(m, "0", "r")
	typeText(m, "ď")
	assertText(t, m, "ďéř")

	sendKeys(m, "0", "~")
	assertText(t, m, "Ďéř")

	sendKeys(m, "0", "2", "x")
	assertText(t, m, "ř")
}

func TestWordsAndYanksWithAccents(t *testing.T) {
	m := testEditor("žlutý kůň běží")
	sendKeys(m, "w")
	assertCursor(t, m, 0, len("žlutý "))
	sendKeys(m, "y", "i", "w")
	if m.yankBuffer != "kůň" {
		t.Fatalf("yiw = %q, want kůň", m.yankBuffer)
	}
	sendKeys(m, "d", "w")
	assertText(t, m, "žlutý běží")
	sendKeys(m, "0", "v", "e", "y")
	if m.yankBuffer != "žlutý" {
		t.Fatalf("visual yank = %q, want žlutý", m.yankBuffer)
	}
	sendKeys(m, "$")
	typeFind := func(keys ...string) { sendKeys(m, keys...) }
	typeFind("0", "f")
	typeText(m, "ě")
	assertCursor(t, m, 0, len("žlutý b"))
	sendKeys(m, "0", "d", "t")
	typeText(m, "b")
	assertText(t, m, "běží")
}

func TestSearchForAccentedText(t *testing.T) {
	m := newReadOnlyEditor("první řádek\ndruhý řádek s čárkou")
	sendKeys(m, "/")
	typeText(m, "čár")
	if m.searchBuffer != "čár" {
		t.Fatalf("search buffer = %q", m.searchBuffer)
	}
	sendKeys(m, "backspace")
	if m.searchBuffer != "čá" {
		t.Fatalf("search buffer after backspace = %q", m.searchBuffer)
	}
	sendKeys(m, "enter")
	assertCursor(t, m, 1, len("druhý řádek s "))
}

func TestRuneBoundaryHelpers(t *testing.T) {
	line := "ač€b" // 1 + 2 + 3 + 1 bytes
	for col, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 3, 4: 3, 5: 3, 6: 6, 7: 7, 99: 7, -1: 0} {
		if got := runeStart(line, col); got != want {
			t.Errorf("runeStart(%d) = %d, want %d", col, got, want)
		}
	}
	for col, want := range map[int]int{0: 1, 1: 3, 2: 3, 3: 6, 5: 6, 6: 7, 7: 7} {
		if got := runeEnd(line, col); got != want {
			t.Errorf("runeEnd(%d) = %d, want %d", col, got, want)
		}
	}
	if got := advanceRunes(line, 0, 3); got != 6 {
		t.Errorf("advanceRunes = %d, want 6", got)
	}
	if !utf8.ValidString(line[:runeStart(line, 4)]) {
		t.Error("slice at runeStart is not valid UTF-8")
	}
}

// Invalid UTF-8 (stray continuation bytes) must not stall the helpers: each
// such byte counts as its own character.
func TestRuneHelpersAlwaysProgressOnInvalidUTF8(t *testing.T) {
	line := "\x92\x80\x80\x80\x80\x80a\xce"
	steps := 0
	for i := 0; i < len(line); i = runeEnd(line, i) {
		if steps++; steps > len(line) {
			t.Fatalf("runeEnd does not advance at %d", i)
		}
	}
	for col := 0; col <= len(line); col++ {
		if s := runeStart(line, col); s > col || col-s >= utf8.UTFMax {
			t.Errorf("runeStart(%d) = %d", col, s)
		}
		if p := prevRuneStart(line, col); col > 0 && p >= col {
			t.Errorf("prevRuneStart(%d) = %d does not move back", col, p)
		}
	}
	if findRuneForward(line, 0, 'a') != 6 || findRuneBackward(line, len(line), 'a') != 6 {
		t.Error("find over invalid bytes failed")
	}
}

// A message body can be one enormous line (minified HTML or JSON). Wrapping it
// used to be quadratic: 500 KB took over seven seconds per frame, freezing
// the reader. The bound here is generous; the single-pass wrap needs ~10 ms.
func TestVeryLongLineRendersQuickly(t *testing.T) {
	e := NewEditor(WithContent(strings.Repeat("lorem ipsum ", 500_000/12)), WithReadOnly(true), WithWrap(true), WithEnableStatusBar(false))
	m := e.(*editorModel)
	e.SetSize(80, 30)
	start := time.Now()
	for range 5 {
		sendKeys(m, "w", "$", "0")
		_ = m.View()
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("5 frames of a 500 KB line took %v", d)
	}
}

func TestWrapRenderedLineKeepsStyleAcrossRows(t *testing.T) {
	got := wrapRenderedLine("\x1b[31mabcdef\x1b[0mgh", 3)
	want := []string{"\x1b[31mabc", "\x1b[31mdef\x1b[0m", "\x1b[0mgh"}
	if len(got) != len(want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	if rows := wrapRenderedLine("čšřžý", 2); len(rows) != 3 || rows[0] != "čš" || rows[2] != "ý" {
		t.Fatalf("multi-byte wrap = %q", rows)
	}
}
