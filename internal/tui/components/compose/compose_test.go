package compose

import (
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
)

// A stored draft can come from an AI client (MCP save_draft); opening it must
// not put its escape sequences or hidden characters on the terminal.
func TestOpeningADraftSanitizesItsContent(t *testing.T) {
	hidden := string(rune(0xE0049)) + string(rune(0xE0047))
	m := New(config.AIConfig{}).SetSize(100, 30)
	m = m.ShowDraft("draft-1", "bob@example.com\x1b]0;pwned\x07", "Hi\x1b[2J"+hidden,
		"first line\x1b]52;c;QUJD\x07\nsecond‮ line"+hidden)

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
