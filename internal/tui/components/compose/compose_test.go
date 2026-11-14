package compose

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
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

func typeInto(m Model, text string) Model {
	for _, r := range text {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestRecipientSuggestions(t *testing.T) {
	contacts := []email.Contact{
		{Name: "Martin Dlužanský", Address: "martin@topol.test", Sent: 3},
		{Name: "Martina Nová", Address: "martina@firma.test", Received: 2},
		{Name: "Evil\x1b[2J", Address: "mallory@evil.test", Received: 1},
	}
	m := New(config.AIConfig{}).SetSize(120, 40).SetContacts(contacts).Show()

	if m = typeInto(m, "m"); len(m.SuggestionsForTest()) != 0 {
		t.Fatal("suggestions after one letter")
	}
	m = typeInto(m, "art")
	if got := m.SuggestionsForTest(); len(got) != 2 || got[0].Address != "martin@topol.test" {
		t.Fatalf("suggestions = %+v", got)
	}
	if view := m.View(); !strings.Contains(view, "Martin Dlužanský <martin@topol.test>") || !strings.Contains(view, "Enter: use suggestion") {
		t.Fatalf("suggestions not shown:\n%s", view)
	}

	// Down selects the second, Enter takes it and leaves room for the next.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	d, _ := m.CurrentDraftForTest()
	if len(d.To) != 1 || d.To[0] != "Martina Nová <martina@firma.test>" || len(m.SuggestionsForTest()) != 0 {
		t.Fatalf("after accepting: to=%q suggestions=%d", d.To, len(m.SuggestionsForTest()))
	}

	// A second recipient: the one already present is not offered again.
	m = typeInto(m, "mart")
	if got := m.SuggestionsForTest(); len(got) != 1 || got[0].Address != "martin@topol.test" {
		t.Fatalf("second suggestions = %+v", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	d, err := m.currentDraft(true)
	if err != nil || len(d.To) != 2 || d.To[1] != "Martin Dlužanský <martin@topol.test>" {
		t.Fatalf("two recipients: %q %v", d.To, err)
	}

	// Tab leaves the field and drops the suggestions; a contact name with an
	// escape sequence is drawn and inserted clean.
	m = typeInto(m, "mall")
	if view := m.View(); strings.Contains(view, "\x1b[2J") {
		t.Fatal("escape sequence from a contact name reached the screen")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if len(m.SuggestionsForTest()) != 0 {
		t.Fatal("suggestions kept after leaving the field")
	}
	// No suggestions outside the recipient fields.
	m = New(config.AIConfig{}).SetSize(120, 40).SetContacts(contacts).Show()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // Cc
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // Bcc
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // Subject
	if m = typeInto(m, "martin"); len(m.SuggestionsForTest()) != 0 {
		t.Fatal("suggestions in the Subject field")
	}
}
