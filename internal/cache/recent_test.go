package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestRecentMessagesPreservesDistinctLegacyMessages(t *testing.T) {
	s := testStore(t)
	const account = "review@example.com"
	seedWithFolders(t, s, "Review", account)

	date := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	for _, uid := range []uint32{1, 2} {
		if err := s.UpsertMessage(account, "Inbox", email.Message{
			UID: uid, From: "notifications@example.com", To: account,
			Subject: "Same notification", Date: date,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := s.RecentMessages([]string{account}, date.Add(-time.Hour), date.Add(time.Hour), 10)
	if len(got) != 2 {
		t.Fatalf("RecentMessages returned %d legacy messages, want 2", len(got))
	}
}

func TestRecentMessagesUsesAbsoluteTimeWindow(t *testing.T) {
	s := testStore(t)
	const account = "review@example.com"
	seedWithFolders(t, s, "Review", account)

	// These timestamps describe the same instant using different offsets.
	messageTime := time.Date(2026, 8, 25, 23, 30, 0, 0, time.FixedZone("CEST", 2*60*60))
	if err := s.UpsertMessage(account, "Inbox", email.Message{
		UID: 1, MessageID: "<offset@example.com>", From: "sender@example.com",
		To: account, Subject: "Offset", Date: messageTime,
	}); err != nil {
		t.Fatal(err)
	}

	since := time.Date(2026, 8, 25, 21, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 25, 22, 0, 0, 0, time.UTC)
	got := s.RecentMessages([]string{account}, since, until, 10)
	if len(got) != 1 || got[0].UID != 1 {
		t.Fatalf("RecentMessages = %+v, want offset-aware UID 1", got)
	}
}
