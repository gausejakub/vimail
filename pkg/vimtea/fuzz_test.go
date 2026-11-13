package vimtea

import (
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// FuzzReadOnlyEditor feeds arbitrary text (a message body, in vimail's reader)
// and arbitrary keys to a read-only editor. It must never panic or hang, and
// must never change the buffer.
func FuzzReadOnlyEditor(f *testing.F) {
	f.Add("hello world\nsecond line", "wv$y", 80, 24)
	f.Add("", "Gggdd", 10, 3)
	f.Add("a\n\n\nb", "}{jkV", 1, 1)
	f.Add("日本語 👩‍💻 e\u0301\ttab", "llllhhe0$", 5, 2)
	f.Add(strings.Repeat("x", 500), "$0fx;,tx", 20, 4)
	f.Add("(a [b {c} d] e)", "yi(ya[%", 0, 0)
	f.Add("one\ntwo", "/o\rnN?t\r", 40, 10)
	f.Add("x", "5j5k9G3wd2wciwxp~J", 40, 10)
	f.Fuzz(func(t *testing.T, content, keys string, width, height int) {
		if len(content) > 4000 || len(keys) > 60 || width < 0 || height < 0 || width > 300 || height > 100 {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			e := NewEditor(WithContent(content), WithReadOnly(true), WithWrap(true), WithEnableStatusBar(false))
			m := e.(*editorModel)
			e.SetSize(width, height)
			before := m.buffer.text()
			_ = m.View()
			for _, r := range keys {
				var km tea.KeyMsg
				switch r {
				case '\r':
					km = tea.KeyMsg{Type: tea.KeyEnter}
				case '\x1b':
					km = tea.KeyMsg{Type: tea.KeyEscape}
				case '\x04':
					km = tea.KeyMsg{Type: tea.KeyCtrlD}
				case '\x15':
					km = tea.KeyMsg{Type: tea.KeyCtrlU}
				default:
					km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
				}
				m.Update(km)
				_ = m.View()
			}
			if got := m.buffer.text(); got != before {
				t.Errorf("read-only buffer changed by keys %q:\n%q\n->\n%q", keys, before, got)
			}
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("editor hung: content=%q keys=%q size=%dx%d", content, keys, width, height)
		}
	})
}

// FuzzEditableEditor is the compose editor: arbitrary starting text (a quoted
// reply, a stored draft, AI output) and arbitrary keys in every mode. It must
// never panic or hang.
func FuzzEditableEditor(f *testing.F) {
	f.Add("hello world\nsecond line", "ddpuu\x12", 80, 24)
	f.Add("", "ihello\x1bVyPp", 10, 3)
	f.Add("a\n\n\nb", "JJJxxx~~>>", 1, 1)
	f.Add("> quoted\n> more", "ggOreply\x1bGdd3x", 30, 5)
	f.Add("(a [b] c)", "ci(X\x1bda[u", 0, 0)
	f.Add("one two three", "wcwx\x1b.2dwD", 12, 2)
	f.Add("x", ":zr\r/x\rnrZvU", 40, 10)
	f.Add("žlutý kůň\nčau 日本語 👍", "wdwxpa\u00e9\x1b~rčvey$hP", 40, 10)
	f.Add("čšř", "llxxiž\x7f\x7fš\x1bfřdTčJ", 10, 3)
	f.Fuzz(func(t *testing.T, content, keys string, width, height int) {
		if len(content) > 4000 || len(keys) > 60 || width < 0 || height < 0 || width > 300 || height > 100 {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			e := NewEditor(WithContent(content), WithWrap(true), WithEnableStatusBar(false))
			validInput := utf8.ValidString(content) && utf8.ValidString(keys)
			m := e.(*editorModel)
			e.SetSize(width, height)
			_ = m.View()
			for _, r := range keys {
				var km tea.KeyMsg
				switch r {
				case '\r':
					km = tea.KeyMsg{Type: tea.KeyEnter}
				case '\x1b':
					km = tea.KeyMsg{Type: tea.KeyEscape}
				case '\x7f':
					km = tea.KeyMsg{Type: tea.KeyBackspace}
				case '\t':
					km = tea.KeyMsg{Type: tea.KeyTab}
				case '\x12':
					km = tea.KeyMsg{Type: tea.KeyCtrlR}
				case '\x04':
					km = tea.KeyMsg{Type: tea.KeyCtrlD}
				default:
					km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
				}
				_, cmd := m.Update(km)
				if cmd != nil {
					// Undo/redo and command mode report back through messages.
					if msg := cmd(); msg != nil {
						if _, isBatch := msg.(tea.BatchMsg); !isBatch {
							m.Update(msg)
						}
					}
				}
				_ = m.View()
				if validInput {
					if text := m.buffer.text(); !utf8.ValidString(text) {
						t.Errorf("key %q split a character: buffer %q (content %q keys %q)", r, text, content, keys)
						return
					}
					line := m.buffer.Line(m.cursor.Row)
					if c := m.cursor.Col; c < len(line) && !utf8.RuneStart(line[c]) {
						t.Errorf("key %q left the cursor inside a character: col %d of %q (content %q keys %q)", r, c, line, content, keys)
						return
					}
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("editor hung: content=%q keys=%q size=%dx%d", content, keys, width, height)
		}
	})
}
