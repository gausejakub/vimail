package compose

import (
	"strings"
	"testing"

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
