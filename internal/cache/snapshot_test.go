package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestReplaceFolderHeadersReconcilesAtomicallyAndPreservesBodies(t *testing.T) {
	store := testStore(t)
	const account = "test@example.com"
	if err := store.SeedAccount("Test", account, "imap.example.com", 993, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureFolder(account, "Inbox"); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []email.Message{
		{UID: 1, MessageID: "<keep@example.com>", Subject: "old subject", Date: time.Now(), Unread: true},
		{UID: 2, MessageID: "<stale@example.com>", Subject: "stale", Date: time.Now()},
	} {
		if err := store.UpsertMessage(account, "Inbox", msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdateMessageBody(account, "Inbox", 1, "cached body", "<p>cached</p>", []email.Attachment{{Filename: "kept.pdf"}}); err != nil {
		t.Fatal(err)
	}

	if err := store.ReplaceFolderHeaders(account, "Inbox", []email.Message{
		{UID: 1, MessageID: "<keep@example.com>", Subject: "new subject", Date: time.Now(), Unread: false},
		{UID: 3, MessageID: "<new@example.com>", Subject: "new", Date: time.Now()},
	}, 42); err != nil {
		t.Fatal(err)
	}
	kept, cached, ok := store.MessageByUID(account, "Inbox", 1)
	if !ok || !cached || kept.Subject != "new subject" || kept.Body != "cached body" || len(kept.Attachments) != 1 {
		t.Fatalf("preserved message = %+v cached=%v found=%v", kept, cached, ok)
	}
	if _, _, ok := store.MessageByUID(account, "Inbox", 2); ok {
		t.Fatal("stale UID survived authoritative snapshot")
	}
	if _, _, ok := store.MessageByUID(account, "Inbox", 3); !ok {
		t.Fatal("new snapshot UID missing")
	}
	if got, err := store.GetUIDValidity(account, "Inbox"); err != nil || got != 42 {
		t.Fatalf("UIDVALIDITY = %d, %v; want 42", got, err)
	}
}
