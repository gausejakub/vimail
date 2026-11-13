package preview

import (
	"runtime/debug"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// FuzzPreviewAndReader draws a hostile message in the preview and the Vim
// reader at several sizes and presses reading keys. Nothing may panic, hang,
// or put a control character on screen.
func FuzzPreviewAndReader(f *testing.F) {
	f.Add("hello\nworld", "", "Subject", "v$y")
	f.Add("", "<p>x</p>", "s", "Gggw")
	f.Add(strings.Repeat("a", 3000), "", "", "$0wbe")
	f.Add("日本語のテキスト\t\u200d👩‍💻 e\u0301 \U0001F1E8\U0001F1FF", "", "x", "lllhVjy")
	f.Add("{\"a\":[1,2,{\"b\":null}]}", "", "", "/a\rn")
	f.Add("a\n\n\n\nb", "", "\x1b]0;t\x07", "}{Gkkkkj")
	f.Fuzz(func(t *testing.T, body, htmlBody, subject, keys string) {
		if len(body) > 20000 || len(htmlBody) > 20000 || len(keys) > 40 {
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
			for _, size := range [][2]int{{80, 24}, {12, 5}, {1, 1}, {0, 0}, {200, 3}} {
				m := New().SetSize(size[0], size[1])
				m, _ = m.Update(util.MessageSelectedMsg{Message: email.Message{
					UID: 1, From: subject, To: subject, Subject: subject, Body: body, HTMLBody: htmlBody,
					Attachments: []email.Attachment{{Filename: subject, Size: len(body)}},
				}})
				for _, k := range []string{"j", "j", "ctrl+d", "G", "k", "g"} {
					m, _ = m.HandleKey(k)
				}
				checkScreen(t, m.View())
				m, _ = m.StartReading()
				checkScreen(t, m.View())
				for _, r := range keys {
					var km tea.KeyMsg
					switch r {
					case '\r':
						km = tea.KeyMsg{Type: tea.KeyEnter}
					case '\x1b':
						km = tea.KeyMsg{Type: tea.KeyEscape}
					default:
						km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
					}
					m, _ = m.ReaderUpdate(km)
					if !m.Reading() {
						break
					}
					checkScreen(t, m.View())
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("render hung: body=%q html=%q keys=%q", body, htmlBody, keys)
		}
	})
}

// checkScreen fails on raw control characters other than newline and the ESC
// that starts the styling sequences the renderer itself emits.
func checkScreen(t *testing.T, view string) {
	t.Helper()
	plain := reANSI.ReplaceAllString(view, "")
	for _, r := range plain {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("control character %U on screen: %q", r, plain)
		}
	}
}
