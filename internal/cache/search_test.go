package cache

import (
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestSearchMessagesDeduplicatesByMessageIDAndKeepsRealFolder(t *testing.T) {
	s := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, s, "Alice", account)
	if _, err := s.EnsureFolder(account, "Archive"); err != nil {
		t.Fatalf("EnsureFolder(Archive): %v", err)
	}

	date := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	first := email.Message{
		UID: 1, MessageID: "<sns-1@example.com>", From: "no-reply@sns.amazonaws.com",
		Subject: "Amazon SES Email Event Notification", Date: date,
	}
	second := first
	second.UID = 2
	second.MessageID = "<sns-2@example.com>"
	copyOfFirst := first
	copyOfFirst.UID = 101

	for _, item := range []struct {
		folder string
		msg    email.Message
	}{{"Inbox", first}, {"Inbox", second}, {"Archive", copyOfFirst}} {
		if err := s.UpsertMessage(account, item.folder, item.msg); err != nil {
			t.Fatalf("UpsertMessage(%s/%d): %v", item.folder, item.msg.UID, err)
		}
	}

	got := s.SearchMessages(account, "Amazon SES", 10)
	if len(got) != 2 {
		t.Fatalf("search returned %d messages, want 2 distinct Message-IDs: %+v", len(got), got)
	}
	seenIDs := make(map[string]bool)
	for _, msg := range got {
		seenIDs[msg.MessageID] = true
		if strings.Contains(msg.Folder, "+") {
			t.Errorf("search returned synthesized folder %q", msg.Folder)
		}
		if _, _, ok := s.MessageByUID(account, msg.Folder, msg.UID); !ok {
			t.Errorf("search handle %s/%d is not directly readable", msg.Folder, msg.UID)
		}
	}
	if !seenIDs[first.MessageID] || !seenIDs[second.MessageID] {
		t.Errorf("search Message-IDs = %v, want both SNS messages", seenIDs)
	}
}

func TestSearchMessagesLegacyFallbackForBlankMessageID(t *testing.T) {
	s := testStore(t)
	const account = "alice@example.com"
	seedWithFolders(t, s, "Alice", account)
	date := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for uid := uint32(1); uid <= 2; uid++ {
		if err := s.UpsertMessage(account, "Inbox", email.Message{
			UID: uid, From: "legacy@example.com", Subject: "legacy duplicate", Date: date,
		}); err != nil {
			t.Fatalf("UpsertMessage(%d): %v", uid, err)
		}
	}
	if got := s.SearchMessages(account, "legacy", 10); len(got) != 1 {
		t.Fatalf("legacy blank-ID fallback returned %d messages, want 1", len(got))
	}
}

func TestSearchMessagesFindsEncryptedCachedBodies(t *testing.T) {
	s := testStore(t)
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey: %v", err)
	}
	s.SetEncryptionKey(key)
	const account = "alice@example.com"
	seedWithFolders(t, s, "Alice", account)
	if err := s.UpsertMessage(account, "Inbox", email.Message{
		UID: 1, MessageID: "<body@example.com>", From: "sender@example.com",
		Subject: "ordinary subject", Date: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}
	if err := s.UpdateMessageBody(account, "Inbox", 1, "needle only in encrypted body", "", nil); err != nil {
		t.Fatalf("UpdateMessageBody: %v", err)
	}

	got := s.SearchMessages(account, "encrypted body", 10)
	if len(got) != 1 || got[0].UID != 1 {
		t.Fatalf("encrypted-body search = %+v, want UID 1", got)
	}
}
