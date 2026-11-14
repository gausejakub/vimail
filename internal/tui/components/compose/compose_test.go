package compose

import (
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

func TestSignaturesFollowTheFromIdentity(t *testing.T) {
	ids := []string{"Jakub <me@x.test>", "Jakub Gause <work@corp.test>"}
	sigs := func(from string) string {
		switch email.AddrSpec(from) {
		case "work@corp.test":
			return "Jakub Gause\nCorp s.r.o."
		case "me@x.test":
			return "J."
		}
		return ""
	}
	body := func(m Model) string { d, _ := m.CurrentDraftForTest(); return d.Body }

	// New message: default identity's signature, with room above it.
	m := New(config.AIConfig{}).SetSize(120, 40).Show().WithIdentities(ids).WithSignatures(sigs)
	if got := body(m); got != "\n\n-- \nJ.\n" {
		t.Fatalf("new message body = %q", got)
	}
	// Untouched, it is still an empty message: Esc closes without a draft.
	m.focused = fieldSubject
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := cmd().(util.ComposeCloseMsg); !ok {
		t.Fatalf("Esc on a signature-only message = %T, want close without saving", cmd())
	}

	// Reply: signature above the quote; changing From swaps it.
	quote := "On Thu, 12 Nov 2026 at 10:00, Petr <p@x.test> wrote:\n> hi\n"
	m = New(config.AIConfig{}).SetSize(120, 40).
		ShowReply(email.Draft{From: ids[1], To: []string{"p@x.test"}, Body: "\n\n" + quote}).WithIdentities(ids).WithSignatures(sigs)
	if got := body(m); !strings.Contains(got, "-- \nJakub Gause\nCorp s.r.o.\n\nOn Thu") {
		t.Fatalf("reply body = %q", got)
	}
	m.focused = fieldFrom
	m.inputs[fieldFrom].SetValue("me@x.test")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // leave From
	if got := body(m); !strings.Contains(got, "-- \nJ.\n\nOn Thu") || strings.Contains(got, "Corp") {
		t.Fatalf("after switching From: %q", got)
	}

	// Once edited, the signature is the user's: switching From leaves it.
	m = m.setBody(strings.Replace(body(m), "J.", "J. (sent from my terminal)", 1))
	m.focused = fieldFrom
	m.inputs[fieldFrom].SetValue("work@corp.test")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := body(m); !strings.Contains(got, "sent from my terminal") || strings.Contains(got, "Corp") {
		t.Fatalf("edited signature replaced: %q", got)
	}

	// A saved draft is opened as it is, not signed a second time.
	saved := "text\n\n-- \nJ.\n"
	m = New(config.AIConfig{}).SetSize(120, 40).ShowDraft(email.Draft{ID: "d1", From: ids[0], Body: saved}).WithIdentities(ids).WithSignatures(sigs)
	if got := body(m); got != strings.TrimRight(saved, "\n") && got != saved {
		t.Fatalf("draft body changed: %q", got)
	}
}
