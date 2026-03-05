package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func testStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open :memory: failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLiteStore(db)
}

// seedWithFolders creates an account and the default folders (Inbox, Sent, Drafts, Trash).
func seedWithFolders(t *testing.T, s *SQLiteStore, name, acctEmail string) {
	t.Helper()
	if err := s.SeedAccount(name, acctEmail, "", 993, "", 587); err != nil {
		t.Fatalf("SeedAccount: %v", err)
	}
	for _, folder := range []string{"Inbox", "Sent", "Drafts", "Trash"} {
		if _, err := s.EnsureFolder(acctEmail, folder); err != nil {
			t.Fatalf("EnsureFolder(%s): %v", folder, err)
		}
	}
}

func TestSeedAccount(t *testing.T) {
	s := testStore(t)

	err := s.SeedAccount("Personal", "alice@example.com", "imap.example.com", 993, "smtp.example.com", 587)
	if err != nil {
		t.Fatalf("SeedAccount: %v", err)
	}

	accts := s.Accounts()
	if len(accts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accts))
	}
	if accts[0].Email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", accts[0].Email)
	}
	if accts[0].Name != "Personal" {
		t.Fatalf("name = %q, want Personal", accts[0].Name)
	}

	// SeedAccount no longer creates default folders — IMAP discovers them.
	folders := s.FoldersFor("alice@example.com")
	if len(folders) != 0 {
		t.Fatalf("got %d folders, want 0 (no default seeding)", len(folders))
	}
}

func TestSeedAccountUpsert(t *testing.T) {
	s := testStore(t)

	s.SeedAccount("Old Name", "alice@example.com", "", 993, "", 587)
	s.SeedAccount("New Name", "alice@example.com", "imap.new.com", 993, "smtp.new.com", 587)

	accts := s.Accounts()
	if len(accts) != 1 {
		t.Fatalf("upsert should not duplicate: got %d", len(accts))
	}
	if accts[0].Name != "New Name" {
		t.Fatalf("name = %q, want New Name", accts[0].Name)
	}
}

func TestMessagesFor(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	msg := email.Message{
		UID:     1,
		From:    "sender@b.com",
		To:      "a@b.com",
		Subject: "Hello",
		Body:    "World",
		Date:    time.Now(),
		Unread:  true,
		Flagged: false,
	}
	if err := s.UpsertMessage("a@b.com", "Inbox", msg); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	msgs := s.MessagesFor("a@b.com", "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Subject != "Hello" {
		t.Fatalf("subject = %q, want Hello", msgs[0].Subject)
	}
	if msgs[0].UID != 1 {
		t.Fatalf("uid = %d, want 1", msgs[0].UID)
	}
	if !msgs[0].Unread {
		t.Fatal("unread should be true")
	}
}

func TestMessagesForEmptyFolder(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	msgs := s.MessagesFor("a@b.com", "Inbox")
	if msgs != nil {
		t.Fatalf("expected nil for empty folder, got %d messages", len(msgs))
	}
}

func TestMessagesForNonexistentFolder(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	msgs := s.MessagesFor("a@b.com", "Nonexistent")
	if msgs != nil {
		t.Fatalf("expected nil for nonexistent folder, got %d messages", len(msgs))
	}
}

func TestDrafts(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	// Save a draft
	draft := email.Message{
		ID:      s.NextDraftID(),
		From:    "a@b.com",
		To:      "someone@b.com",
		Subject: "Draft subject",
		Body:    "Draft body",
		Date:    time.Now(),
	}
	s.SaveDraft("a@b.com", draft)

	// Retrieve via MessagesFor
	msgs := s.MessagesFor("a@b.com", "Drafts")
	if len(msgs) != 1 {
		t.Fatalf("got %d drafts, want 1", len(msgs))
	}
	if msgs[0].Subject != "Draft subject" {
		t.Fatalf("subject = %q, want Draft subject", msgs[0].Subject)
	}

	// Folder unread count should reflect drafts
	folders := s.FoldersFor("a@b.com")
	for _, f := range folders {
		if f.Name == "Drafts" {
			if f.UnreadCount != 1 {
				t.Fatalf("Drafts unread = %d, want 1", f.UnreadCount)
			}
		}
	}

	// Update draft
	draft.Subject = "Updated subject"
	s.SaveDraft("a@b.com", draft)
	msgs = s.MessagesFor("a@b.com", "Drafts")
	if len(msgs) != 1 {
		t.Fatalf("after update: got %d drafts, want 1", len(msgs))
	}
	if msgs[0].Subject != "Updated subject" {
		t.Fatalf("subject = %q, want Updated subject", msgs[0].Subject)
	}

	// Delete draft
	s.DeleteDraft("a@b.com", draft.ID)
	msgs = s.MessagesFor("a@b.com", "Drafts")
	if len(msgs) != 0 {
		t.Fatalf("after delete: got %d drafts, want 0", len(msgs))
	}
}

func TestNextDraftID(t *testing.T) {
	s := testStore(t)

	id1 := s.NextDraftID()
	id2 := s.NextDraftID()
	if id1 == id2 {
		t.Fatal("NextDraftID should return unique IDs")
	}
}

func TestFolderUnreadCount(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	// Add 2 unread and 1 read message
	for i, unread := range []bool{true, true, false} {
		s.UpsertMessage("a@b.com", "Inbox", email.Message{
			UID:    uint32(i + 1),
			From:   "x@b.com",
			To:     "a@b.com",
			Date:   time.Now(),
			Unread: unread,
		})
	}

	folders := s.FoldersFor("a@b.com")
	for _, f := range folders {
		if f.Name == "Inbox" {
			if f.UnreadCount != 2 {
				t.Fatalf("Inbox unread = %d, want 2", f.UnreadCount)
			}
		}
	}
}

func TestEnsureFolder(t *testing.T) {
	s := testStore(t)
	s.SeedAccount("Test", "a@b.com", "", 993, "", 587)

	id, err := s.EnsureFolder("a@b.com", "Archive")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if id <= 0 {
		t.Fatalf("folder id = %d, want > 0", id)
	}

	// Calling again should return same ID
	id2, err := s.EnsureFolder("a@b.com", "Archive")
	if err != nil {
		t.Fatalf("EnsureFolder again: %v", err)
	}
	if id != id2 {
		t.Fatalf("ids differ: %d vs %d", id, id2)
	}
}

func TestUIDValidity(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	val, err := s.GetUIDValidity("a@b.com", "Inbox")
	if err != nil {
		t.Fatalf("GetUIDValidity: %v", err)
	}
	if val != 0 {
		t.Fatalf("initial uidvalidity = %d, want 0", val)
	}

	s.SetUIDValidity("a@b.com", "Inbox", 12345)
	val, _ = s.GetUIDValidity("a@b.com", "Inbox")
	if val != 12345 {
		t.Fatalf("uidvalidity = %d, want 12345", val)
	}
}

func TestPurgeFolder(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	s.UpsertMessage("a@b.com", "Inbox", email.Message{UID: 1, From: "x", To: "y", Date: time.Now()})
	s.UpsertMessage("a@b.com", "Inbox", email.Message{UID: 2, From: "x", To: "y", Date: time.Now()})

	msgs := s.MessagesFor("a@b.com", "Inbox")
	if len(msgs) != 2 {
		t.Fatalf("before purge: %d messages", len(msgs))
	}

	s.PurgeFolder("a@b.com", "Inbox")
	msgs = s.MessagesFor("a@b.com", "Inbox")
	if msgs != nil {
		t.Fatalf("after purge: %d messages, want 0", len(msgs))
	}
}

func TestHighestUID(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	uid, _ := s.HighestUID("a@b.com", "Inbox")
	if uid != 0 {
		t.Fatalf("empty folder highest uid = %d, want 0", uid)
	}

	s.UpsertMessage("a@b.com", "Inbox", email.Message{UID: 10, From: "x", To: "y", Date: time.Now()})
	s.UpsertMessage("a@b.com", "Inbox", email.Message{UID: 5, From: "x", To: "y", Date: time.Now()})

	uid, _ = s.HighestUID("a@b.com", "Inbox")
	if uid != 10 {
		t.Fatalf("highest uid = %d, want 10", uid)
	}
}

func TestUpdateMessageBody(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Test", "a@b.com")

	s.UpsertMessage("a@b.com", "Inbox", email.Message{UID: 1, From: "x", To: "y", Date: time.Now(), Body: ""})
	s.UpdateMessageBody("a@b.com", "Inbox", 1, "Full body text", "<p>Full body text</p>")

	msgs := s.MessagesFor("a@b.com", "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}
	if msgs[0].Body != "Full body text" {
		t.Fatalf("body = %q, want Full body text", msgs[0].Body)
	}
	if msgs[0].HTMLBody != "<p>Full body text</p>" {
		t.Fatalf("html_body = %q, want <p>Full body text</p>", msgs[0].HTMLBody)
	}
}

func TestMultipleAccounts(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Personal", "alice@a.com")
	seedWithFolders(t, s, "Work", "alice@b.com")

	accts := s.Accounts()
	if len(accts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accts))
	}

	// Each account has its own folders
	f1 := s.FoldersFor("alice@a.com")
	f2 := s.FoldersFor("alice@b.com")
	if len(f1) != len(f2) {
		t.Fatalf("folder counts differ: %d vs %d", len(f1), len(f2))
	}

	// Messages are isolated per account
	s.UpsertMessage("alice@a.com", "Inbox", email.Message{UID: 1, From: "x", To: "y", Date: time.Now()})
	m1 := s.MessagesFor("alice@a.com", "Inbox")
	m2 := s.MessagesFor("alice@b.com", "Inbox")
	if len(m1) != 1 {
		t.Fatalf("alice@a.com inbox: %d messages, want 1", len(m1))
	}
	if m2 != nil {
		t.Fatalf("alice@b.com inbox: %d messages, want 0", len(m2))
	}
}
