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
