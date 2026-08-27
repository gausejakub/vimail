package mailbox

import (
	"testing"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
)

func TestVimCountMotions(t *testing.T) {
	m := New(email.NewMockStore()).SetSize(24, 5)

	for _, key := range []string{"7", "j"} {
		m, _ = m.HandleKey(key)
	}
	if m.cursor != 7 {
		t.Fatalf("7j cursor = %d, want 7", m.cursor)
	}
	if m.offset != 3 {
		t.Fatalf("7j offset = %d, want 3", m.offset)
	}

	for _, key := range []string{"3", "k"} {
		m, _ = m.HandleKey(key)
	}
	if m.cursor != 4 {
		t.Fatalf("3k cursor = %d, want 4", m.cursor)
	}
	if m.offset > m.cursor || m.cursor >= m.offset+m.height {
		t.Fatalf("cursor %d is outside visible range [%d,%d)", m.cursor, m.offset, m.offset+m.height)
	}
}

func TestVimJumpMotions(t *testing.T) {
	m := New(email.NewMockStore()).SetSize(24, 5)

	m, _ = m.HandleKey("G")
	if m.cursor != len(m.items)-1 {
		t.Fatalf("G cursor = %d, want %d", m.cursor, len(m.items)-1)
	}

	m, _ = m.HandleKey("g")
	if m.cursor != len(m.items)-1 {
		t.Fatal("first g should wait for the second g")
	}
	m, _ = m.HandleKey("g")
	if m.cursor != 0 {
		t.Fatalf("gg cursor = %d, want 0", m.cursor)
	}
	if m.offset != 0 {
		t.Fatalf("gg offset = %d, want 0", m.offset)
	}

	for _, key := range []string{"7", "G"} {
		m, _ = m.HandleKey(key)
	}
	if m.cursor != 6 {
		t.Fatalf("7G cursor = %d, want 6", m.cursor)
	}
}

func TestMotionSelectsFolderAndKeepsCursorVisible(t *testing.T) {
	m := New(email.NewMockStore()).SetSize(24, 4)

	m, _ = m.HandleKey("6")
	m, selectCmd := m.HandleKey("j")
	if selectCmd == nil {
		t.Fatal("folder motion should emit FolderSelectedMsg")
	}
	msg := selectCmd()
	selected, ok := msg.(util.FolderSelectedMsg)
	if !ok {
		t.Fatalf("motion command emitted %T, want FolderSelectedMsg", msg)
	}
	if selected.Account != "alice@acme.corp" || selected.Folder != "Inbox" {
		t.Fatalf("selected = %s/%s, want alice@acme.corp/Inbox", selected.Account, selected.Folder)
	}
	if m.cursor < m.offset || m.cursor >= m.offset+m.height {
		t.Fatalf("cursor %d is outside visible range [%d,%d)", m.cursor, m.offset, m.offset+m.height)
	}
}
