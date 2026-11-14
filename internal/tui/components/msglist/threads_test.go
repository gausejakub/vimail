package msglist

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// threadStore serves one folder of messages, newest first.
type threadStore struct {
	*email.MockStore
	msgs []email.Message
}

func (s *threadStore) MessagesForPage(_, _ string, offset, limit int) []email.Message {
	if offset >= len(s.msgs) {
		return nil
	}
	end := min(offset+limit, len(s.msgs))
	return append([]email.Message(nil), s.msgs[offset:end]...)
}
func (s *threadStore) MessageCount(_, _ string) int { return len(s.msgs) }

func conversation() []email.Message {
	at := func(h int) time.Time { return time.Date(2026, 11, 14, h, 0, 0, 0, time.UTC) }
	mk := func(uid uint32, subject, thread string, hour int) email.Message {
		return email.Message{ID: fmt.Sprint(uid), UID: uid, From: "Petr <p@x.test>", Subject: subject, ThreadID: thread, Date: at(hour)}
	}
	return []email.Message{ // newest first
		mk(7, "Re: Digifest", "root-a", 17),
		mk(6, "Lunch", "root-b", 16),
		mk(5, "Invoice", "", 15),
		mk(4, "Re: Digifest", "root-a", 14),
		mk(2, "Re: Lunch", "root-b", 12),
		mk(1, "Digifest", "root-a", 11),
	}
}

func load(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := m.Update(util.FolderSelectedMsg{Account: "me@x.test", Folder: "Inbox"})
	m, _ = m.Update(cmd())
	return m
}

func order(m Model) string {
	var ids []string
	for _, msg := range m.messages {
		ids = append(ids, msg.ID)
	}
	return strings.Join(ids, ",")
}

func TestThreadedListGroupsConversations(t *testing.T) {
	store := &threadStore{MockStore: email.NewMockStore(), msgs: conversation()}
	m := load(t, New(store).SetSize(100, 20).Focus())
	if got := order(m); got != "7,6,5,4,2,1" {
		t.Fatalf("flat order = %s", got)
	}

	// Move to "Re: Lunch" (uid 2), then group: the cursor stays on it.
	for i := 0; i < 4; i++ {
		m, _ = m.HandleKey("j")
	}
	m = m.SetThreaded(true)
	if got := order(m); got != "7,4,1,6,2,5" {
		t.Fatalf("grouped order = %s", got)
	}
	if sel := m.SelectedMessage(); sel == nil || sel.UID != 2 {
		t.Fatalf("cursor moved to %+v", sel)
	}
	view := m.View()
	for _, want := range []string{"(3) Re: Digifest", "↳ Re: Digifest", "↳ Digifest", "(2) Lunch", "↳ Re: Lunch"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "(1)") || strings.Contains(view, "↳ Invoice") {
		t.Errorf("single message marked as a conversation:\n%s", view)
	}

	// A refresh with a new reply keeps the grouping and the cursor's message.
	store.msgs = append([]email.Message{{ID: "8", UID: 8, From: "Eva <e@x.test>", Subject: "Re: Lunch", ThreadID: "root-b", Date: time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)}}, store.msgs...)
	m, _ = m.Update(util.FolderRefreshMsg{Account: "me@x.test", Folder: "Inbox"})
	if got := order(m); got != "8,6,2,7,4,1,5" {
		t.Fatalf("after refresh = %s", got)
	}
	if sel := m.SelectedMessage(); sel == nil || sel.UID != 2 {
		t.Fatalf("cursor after refresh on %+v", sel)
	}

	// A visual selection covers the rows as shown.
	m = m.SetCursor(0).EnterVisual()
	m, _ = m.HandleKey("j")
	m, _ = m.HandleKey("j")
	var picked []string
	for _, msg := range m.SelectedMessages() {
		picked = append(picked, msg.ID)
	}
	if got := strings.Join(picked, ","); got != "8,6,2" {
		t.Fatalf("visual selection = %s", got)
	}
	m = m.ExitVisual()

	// Back to flat: date order again, cursor still on its message.
	m = m.SetCursor(2).SetThreaded(false)
	if got := order(m); got != "8,7,6,5,4,2,1" {
		t.Fatalf("flat again = %s", got)
	}
	if sel := m.SelectedMessage(); sel == nil || sel.UID != 2 {
		t.Fatalf("cursor after ungrouping on %+v", sel)
	}
}

// A folder longer than the loaded window stays flat: a conversation
// reaching outside the window would be shown in pieces.
func TestLongFoldersStayFlat(t *testing.T) {
	var msgs []email.Message
	for i := pageSize + 50; i > 0; i-- {
		msgs = append(msgs, email.Message{ID: fmt.Sprint(i), UID: uint32(i), Subject: "s", ThreadID: fmt.Sprint(i % 3),
			Date: time.Unix(int64(i)*60, 0)})
	}
	store := &threadStore{MockStore: email.NewMockStore(), msgs: msgs}
	m := load(t, New(store).SetSize(100, 20).SetThreaded(true).Focus())
	if m.threadPos != nil || m.messages[0].UID != uint32(pageSize+50) || m.messages[1].UID != uint32(pageSize+49) {
		t.Fatalf("long folder was grouped: first uids %d, %d", m.messages[0].UID, m.messages[1].UID)
	}
	_ = tea.Quit
}
