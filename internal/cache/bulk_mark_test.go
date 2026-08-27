package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestMarkReadUIDsIsAtomicAndCascadesLabelCopies(t *testing.T) {
	s := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, s, "Alice", account)
	if _, err := s.EnsureFolder(account, "Archive"); err != nil {
		t.Fatalf("EnsureFolder(Archive): %v", err)
	}
	date := time.Now()
	for _, item := range []struct {
		folder    string
		uid       uint32
		messageID string
	}{{"Inbox", 1, "<one@example.com>"}, {"Inbox", 2, "<two@example.com>"}, {"Archive", 101, "<one@example.com>"}} {
		if err := s.UpsertMessage(account, item.folder, email.Message{
			UID: item.uid, MessageID: item.messageID, From: "sender@example.com",
			Subject: "subject", Date: date, Unread: true,
		}); err != nil {
			t.Fatalf("UpsertMessage(%s/%d): %v", item.folder, item.uid, err)
		}
	}

	if err := s.MarkReadUIDs(account, "Inbox", []uint32{1, 2}); err != nil {
		t.Fatalf("MarkReadUIDs: %v", err)
	}
	for _, item := range []struct {
		folder string
		uid    uint32
	}{{"Inbox", 1}, {"Inbox", 2}, {"Archive", 101}} {
		msg, _, ok := s.MessageByUID(account, item.folder, item.uid)
		if !ok || msg.Unread {
			t.Errorf("%s/%d = found %v unread %v, want read", item.folder, item.uid, ok, msg.Unread)
		}
	}

	// A missing member rolls the transaction back rather than partially
	// applying the valid prefix.
	msg, _, _ := s.MessageByUID(account, "Inbox", 2)
	msg.Unread = true
	if err := s.UpsertMessage(account, "Inbox", msg); err != nil {
		t.Fatalf("reset UID 2 unread: %v", err)
	}
	if err := s.MarkReadUIDs(account, "Inbox", []uint32{2, 999}); err == nil {
		t.Fatal("MarkReadUIDs with missing UID unexpectedly succeeded")
	}
	msg, _, _ = s.MessageByUID(account, "Inbox", 2)
	if !msg.Unread {
		t.Fatal("failed batch partially marked UID 2 read")
	}
}

func TestMarkAllReadIsAtomicAndCascadesLabelCopies(t *testing.T) {
	s := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, s, "Alice", account)
	if _, err := s.EnsureFolder(account, "Archive"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		folder    string
		uid       uint32
		messageID string
	}{
		{"Inbox", 1, "<shared@example.com>"},
		{"Inbox", 2, "<inbox@example.com>"},
		{"Archive", 101, "<shared@example.com>"},
		{"Trash", 201, "<trash@example.com>"},
	} {
		if err := s.UpsertMessage(account, item.folder, email.Message{
			UID: item.uid, MessageID: item.messageID, From: "sender@example.com",
			Subject: "subject", Date: time.Now(), Unread: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	count, err := s.MarkAllRead(account, "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("Inbox changed count = %d, want 2", count)
	}
	count, err = s.MarkAllRead(account, "Trash")
	if err != nil || count != 1 {
		t.Fatalf("Trash changed count = %d, err=%v; want 1", count, err)
	}
	for _, item := range []struct {
		folder string
		uid    uint32
	}{{"Inbox", 1}, {"Inbox", 2}, {"Archive", 101}, {"Trash", 201}} {
		msg, _, ok := s.MessageByUID(account, item.folder, item.uid)
		if !ok || msg.Unread {
			t.Errorf("%s/%d unread after MarkAllRead", item.folder, item.uid)
		}
	}
}
