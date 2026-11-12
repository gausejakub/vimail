package preview

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/pkg/vimtea"
)

func readerModel(t *testing.T) Model {
	t.Helper()
	m := New().SetSize(80, 20)
	m, _ = m.Update(util.MessageSelectedMsg{Message: email.Message{
		UID: 1, From: "Alice <alice@example.com>", To: "me@example.com", Subject: "Hello",
		Date: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC),
		Body: "Tracking code: ABC-123\nSecond line",
	}})
	m, _ = m.StartReading()
	if !m.Reading() {
		t.Fatal("reader did not open")
	}
	return m
}

func press(m Model, keys ...string) (Model, []tea.Msg) {
	var out []tea.Msg
	var walk func(tea.Msg)
	walk = func(msg tea.Msg) {
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					walk(c())
				}
			}
		case nil:
		default:
			out = append(out, msg)
		}
	}
	for _, k := range keys {
		var km tea.KeyMsg
		switch k {
		case "esc":
			km = tea.KeyMsg{Type: tea.KeyEscape}
		default:
			km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		var cmd tea.Cmd
		m, cmd = m.ReaderUpdate(km)
		if cmd != nil {
			walk(cmd())
		}
	}
	return m, out
}

func yanks(msgs []tea.Msg) []string {
	var out []string
	for _, msg := range msgs {
		if y, ok := msg.(vimtea.YankMsg); ok {
			out = append(out, y.Text)
		}
	}
	return out
}

func TestReaderContentHasHeadersAndBody(t *testing.T) {
	m := readerModel(t)
	got := m.reader.GetBuffer().Text()
	for _, want := range []string{"From: Alice <alice@example.com>", "Subject: Hello", "Tracking code: ABC-123"} {
		if !strings.Contains(got, want) {
			t.Errorf("reader content missing %q:\n%s", want, got)
		}
	}
}

func TestReaderYanksToClipboardMessage(t *testing.T) {
	m := readerModel(t)
	// Line 6 is the first body line ("Tracking code: ABC-123"); yank its last WORD.
	m, msgs := press(m, "6", "G", "$", "y", "i", "W")
	if got := yanks(msgs); len(got) != 1 || got[0] != "ABC-123" {
		t.Fatalf("yanks = %q, want [ABC-123]", got)
	}
	_, msgs = press(m, "V", "j", "y")
	if got := yanks(msgs); len(got) != 1 || !strings.Contains(got[0], "Second line") {
		t.Fatalf("visual line yank = %q", got)
	}
}

func TestReaderIsReadOnlyAndClosesWithQ(t *testing.T) {
	m := readerModel(t)
	before := m.reader.GetBuffer().Text()
	m, _ = press(m, "d", "d", "x", "i", "Z", "esc")
	if !m.Reading() {
		// esc after the refused insert is a normal-mode esc and closes the reader.
		m = readerModel(t)
	}
	if got := m.reader.GetBuffer().Text(); got != before {
		t.Fatalf("reader buffer changed:\n%s", got)
	}

	m, _ = press(m, "v", "esc")
	if !m.Reading() {
		t.Fatal("esc in visual mode should only end the selection")
	}
	m, _ = press(m, "q")
	if m.Reading() {
		t.Fatal("q in normal mode should close the reader")
	}
}

func TestSelectingAnotherMessageClosesReader(t *testing.T) {
	m := readerModel(t)
	m, _ = m.Update(util.MessageSelectedMsg{Message: email.Message{UID: 2, Subject: "Other"}})
	if m.Reading() {
		t.Fatal("reader stayed open for a different message")
	}
}

func TestReaderViewFitsPaneWithHintLine(t *testing.T) {
	m := readerModel(t)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("reader view has %d lines, pane is %d", len(lines), m.height)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "NORMAL") || !strings.Contains(last, "y copy") || strings.Count(last, "NORMAL") != 1 {
		t.Fatalf("hint line = %q", last)
	}
	m, _ = press(m, "/", "S", "e", "c")
	if last := lastLine(m.View()); !strings.Contains(last, "/Sec") {
		t.Fatalf("search prompt not shown: %q", last)
	}
}

func lastLine(v string) string {
	lines := strings.Split(v, "\n")
	return lines[len(lines)-1]
}

func TestReaderHintDoesNotRepeatMode(t *testing.T) {
	for status, want := range map[string]string{
		" VISUAL | -- VISUAL --":        "v/V select · y copy · / search · q back",
		" NORMAL":                       "v/V select · y copy · / search · q back",
		"/Sec":                          "/Sec",
		" NORMAL | yanked 5 characters": "yanked 5 characters",
	} {
		mode := "NORMAL"
		if strings.Contains(status, "VISUAL") {
			mode = "VISUAL"
		}
		if got := readerHint(status, mode); got != want {
			t.Errorf("readerHint(%q) = %q, want %q", status, got, want)
		}
	}
}
