package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestRestoreMessagesMovesCachedMessageToServerAssignedUID(t *testing.T) {
	store := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, store, "Alice", account)
	message := email.Message{
		UID: 7, MessageID: "<restore@example.com>", From: "sender@example.com",
		To: account, Subject: "Restore me", Date: time.Now(), Unread: true, Flagged: true,
	}
	if err := store.UpsertMessage(account, "Trash", message); err != nil {
		t.Fatal(err)
	}
	attachments := []email.Attachment{{Filename: "contract.pdf", ContentType: "application/pdf", Size: 1234, PartNum: "2"}}
	if err := store.UpdateMessageBody(account, "Trash", 7, "cached body", "<p>cached body</p>", attachments); err != nil {
		t.Fatal(err)
	}

	if err := store.RestoreMessages(account, "Trash", "Inbox", []UIDMove{{Source: 7, Destination: 42}}); err != nil {
		t.Fatalf("RestoreMessages: %v", err)
	}
	if _, _, ok := store.MessageByUID(account, "Trash", 7); ok {
		t.Fatal("source Trash row still exists")
	}
	restored, bodyCached, ok := store.MessageByUID(account, "Inbox", 42)
	if !ok {
		t.Fatal("destination Inbox row missing")
	}
	if restored.MessageID != message.MessageID || restored.Body != "cached body" || !bodyCached || !restored.Flagged {
		t.Fatalf("restored message = %+v cached=%v", restored, bodyCached)
	}
	if len(restored.Attachments) != 1 || restored.Attachments[0].Filename != "contract.pdf" {
		t.Fatalf("restored attachments = %+v", restored.Attachments)
	}
}

func TestRestoreMessagesIsAtomicWhenSourceUIDIsMissing(t *testing.T) {
	store := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, store, "Alice", account)
	if err := store.UpsertMessage(account, "Trash", email.Message{
		UID: 7, MessageID: "<restore@example.com>", From: "sender@example.com",
		To: account, Subject: "Restore me", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	err := store.RestoreMessages(account, "Trash", "Inbox", []UIDMove{
		{Source: 7, Destination: 42},
		{Source: 999, Destination: 43},
	})
	if err == nil {
		t.Fatal("RestoreMessages succeeded with a missing source UID")
	}
	if _, _, ok := store.MessageByUID(account, "Trash", 7); !ok {
		t.Fatal("valid source row moved despite transaction rollback")
	}
	if _, _, ok := store.MessageByUID(account, "Inbox", 42); ok {
		t.Fatal("destination row survived transaction rollback")
	}
}
