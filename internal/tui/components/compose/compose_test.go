package compose

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// A stored draft can come from an AI client (MCP save_draft); opening it must
// not put its escape sequences or hidden characters on the terminal.
func TestOpeningADraftSanitizesItsContent(t *testing.T) {
	hidden := string(rune(0xE0049)) + string(rune(0xE0047))
	m := New(config.AIConfig{}).SetSize(100, 30)
	m = m.ShowDraft(email.Draft{ID: "draft-1", To: []string{"bob@example.com\x1b]0;pwned\x07"}, Subject: "Hi\x1b[2J" + hidden,
		Body: "first line\x1b]52;c;QUJD\x07\nsecond‮ line" + hidden})

	view := m.View()
	for _, bad := range []string{"\x1b]52", "\x1b]0;", "\x1b[2J", "‮", hidden} {
		if strings.Contains(view, bad) {
			t.Errorf("compose view contains %q", bad)
		}
	}
	if !strings.Contains(view, "first line") || !strings.Contains(view, "second line") {
		t.Errorf("draft text missing from view")
	}
}

func TestMarkdownToggle(t *testing.T) {
	m := New(config.AIConfig{}).SetSize(120, 40).Show().WithMarkdownDefault(true)
	if d, _ := m.CurrentDraftForTest(); !d.Markdown || !strings.Contains(m.View(), "Markdown") {
		t.Fatalf("account default not applied: %+v", d)
	}
	m, _ = m.Update(markdownMsg{}) // what :markdown sends
	if d, _ := m.CurrentDraftForTest(); d.Markdown || strings.Contains(m.View(), "· Markdown") {
		t.Fatal(":markdown did not switch to plain text")
	}
	// A saved draft keeps its own setting whatever the account default is.
	m = New(config.AIConfig{}).SetSize(120, 40).ShowDraft(email.Draft{ID: "d1", Body: "x", Markdown: false}).WithMarkdownDefault(true)
	if d, _ := m.CurrentDraftForTest(); d.Markdown {
		t.Fatal("saved plain draft became Markdown")
	}
}

func TestSignAndEncryptToggles(t *testing.T) {
	// OpenPGP off: the commands say so and change nothing.
	m := New(config.AIConfig{}).SetSize(120, 40).Show()
	m, _ = m.Update(pgpMsg{encrypt: true})
	if d, _ := m.CurrentDraftForTest(); d.Encrypt || d.Sign {
		t.Fatal("encrypt switched on without OpenPGP")
	}

	// On, with account defaults: signed, not encrypted.
	var checked []email.Draft
	missingKey := false
	check := func(d email.Draft) error {
		checked = append(checked, d)
		if missingKey {
			return errors.New("no usable public key for bob@x.test")
		}
		return nil
	}
	m = New(config.AIConfig{}).SetSize(120, 40).Show().WithPGP(true, false, check)
	if d, _ := m.CurrentDraftForTest(); !d.Sign || d.Encrypt || !strings.Contains(m.View(), "· signed") {
		t.Fatalf("defaults not applied: %+v", d)
	}
	m, _ = m.Update(pgpMsg{encrypt: true})
	m, _ = m.Update(pgpMsg{})
	if d, _ := m.CurrentDraftForTest(); d.Sign || !d.Encrypt || !strings.Contains(m.View(), "· encrypted") {
		t.Fatalf("toggles: %+v", d)
	}

	// Sending with a missing key keeps the composer open and reports it.
	m = typeInto(m, "bob@x.test")
	missingKey = true
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if info, ok := cmd().(util.InfoMsg); !ok || !info.IsError || !strings.Contains(info.Text, "bob@x.test") || !m.Visible() {
		t.Fatalf("missing key: %#v visible=%v", cmd(), m.Visible())
	}
	missingKey = false
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if submit, ok := cmd().(util.ComposeSubmitMsg); !ok || !submit.Draft.Encrypt || len(checked) < 2 {
		t.Fatalf("send with keys present: %#v", cmd())
	}
	// A saved draft keeps its own choice.
	m = New(config.AIConfig{}).SetSize(120, 40).ShowDraft(email.Draft{ID: "d", Body: "x"}).WithPGP(true, true, check)
	if d, _ := m.CurrentDraftForTest(); d.Sign || d.Encrypt {
		t.Fatal("account defaults overrode a saved draft")
	}
}

func typeInto(m Model, text string) Model {
	for _, r := range text {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}
