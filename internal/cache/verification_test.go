package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestMessageStateWritesCascadeAndMoveOptimistically(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Alice", "alice@example.com")
	_, _ = s.EnsureFolder("alice@example.com", "Archive")
	for _, item := range []struct {
		folder string
		uid    uint32
	}{{"Inbox", 1}, {"Archive", 9}} {
		if err := s.UpsertMessage("alice@example.com", item.folder, email.Message{UID: item.uid, MessageID: "<same>", Date: time.Now(), Unread: false}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertMessage("alice@example.com", "Sent", email.Message{UID: 1, MessageID: "<existing>", Subject: "existing", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUnreadUIDs("alice@example.com", "Inbox", []uint32{1}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFlaggedUIDs("alice@example.com", "Inbox", []uint32{1}, true); err != nil {
		t.Fatal(err)
	}
	copy, _, ok := s.MessageByUID("alice@example.com", "Archive", 9)
	if !ok || !copy.Unread || !copy.Flagged {
		t.Fatalf("label copy state = %+v, want unread+flagged", copy)
	}
	cacheUIDs, err := s.MoveMessagesLocal("alice@example.com", "Inbox", "Sent", []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.MessageByUID("alice@example.com", "Inbox", 1); ok {
		t.Fatal("source row remains after optimistic move")
	}
	if len(cacheUIDs) != 1 || cacheUIDs[0] == 1 {
		t.Fatalf("collision-safe cache UIDs=%v", cacheUIDs)
	}
	existing, _, ok := s.MessageByUID("alice@example.com", "Sent", 1)
	if !ok || existing.Subject != "existing" {
		t.Fatalf("existing destination row overwritten: %+v found=%v", existing, ok)
	}
	if _, _, ok := s.MessageByUID("alice@example.com", "Sent", cacheUIDs[0]); !ok {
		t.Fatal("destination row missing after optimistic move")
	}
}

func TestIntegrityOrphanSweepAndDecryptSample(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Alice", "alice@example.com")
	if err := s.IntegrityCheck(false); err != nil {
		t.Fatal(err)
	}
	_, _ = s.DB().Exec(`PRAGMA foreign_keys=OFF`)
	if _, err := s.DB().Exec(`INSERT INTO attachments(folder_id,uid,filename) VALUES(999,999,'orphan')`); err != nil {
		t.Fatal(err)
	}
	defer s.DB().Exec(`PRAGMA foreign_keys=ON`)
	result, err := s.SweepOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if result.Attachments != 1 {
		t.Fatalf("attachments removed=%d, want 1", result.Attachments)
	}
	key, _ := GenerateEncryptionKey()
	s.SetEncryptionKey(key)
	if err := s.UpsertMessage("alice@example.com", "Inbox", email.Message{UID: 1, Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMessageBody("alice@example.com", "Inbox", 1, "secret", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SampleDecrypt(5); err != nil {
		t.Fatal(err)
	}
	s.SetEncryptionKey([]byte("wrong wrong wrong wrong wrong wrong!!"))
	if err := s.SampleDecrypt(5); err == nil {
		t.Fatal("wrong encryption key was not detected")
	}
}

func TestPendingUIDsIncludesNewWriteTypes(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Alice", "alice@example.com")
	_, _ = s.QueueOp(OpFlag, "alice@example.com", "Inbox", FlagPayload{UIDs: []uint32{2}, Flagged: true})
	_, _ = s.QueueOp(OpMove, "alice@example.com", "Inbox", MovePayload{UIDs: []uint32{3}, Destination: "Sent"})
	got := s.PendingUIDs("alice@example.com", "Inbox")
	if !got[2] || !got[3] {
		t.Fatalf("pending UIDs=%v", got)
	}
	if destination := s.PendingUIDs("alice@example.com", "Sent"); !destination[3] {
		t.Fatalf("move destination pending UIDs=%v", destination)
	}
}

// Placeholders must stay out of HighestUID (or incremental sync would skip
// new mail), and reconciling them must survive the server UID already being
// cached by a sync that ran before the move op completed.
func TestMovePlaceholdersIgnoredByHighestUIDAndReconcileCollisions(t *testing.T) {
	s := testStore(t)
	const acct = "alice@example.com"
	seedWithFolders(t, s, "Alice", acct)
	_, _ = s.EnsureFolder(acct, "Archive")
	for _, uid := range []uint32{10, 11} {
		if err := s.UpsertMessage(acct, "Inbox", email.Message{UID: uid, Subject: "moving", Date: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertMessage(acct, "Archive", email.Message{UID: 5, Subject: "old", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cacheUIDs, err := s.MoveMessagesLocal(acct, "Inbox", "Archive", []uint32{10, 11})
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range cacheUIDs {
		if uid < PlaceholderUIDBase {
			t.Fatalf("placeholder UID %d outside reserved range", uid)
		}
	}
	if high, _ := s.HighestUID(acct, "Archive"); high != 5 {
		t.Fatalf("HighestUID = %d, want 5 (placeholders ignored)", high)
	}
	// An incremental sync fetched server UID 12 before reconciliation.
	if err := s.UpsertMessage(acct, "Archive", email.Message{UID: 12, Subject: "synced copy", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileMovedUIDs(acct, "Archive", []UIDMove{{Source: cacheUIDs[0], Destination: 11}, {Source: cacheUIDs[1], Destination: 12}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, uid := range []uint32{11, 12} {
		msg, _, ok := s.MessageByUID(acct, "Archive", uid)
		if !ok || msg.Subject != "moving" {
			t.Fatalf("Archive/%d = %+v found=%v, want reconciled moved row", uid, msg, ok)
		}
	}
	if n := s.MessageCount(acct, "Archive"); n != 3 {
		t.Fatalf("Archive count = %d, want 3", n)
	}
}

func TestPendingUIDsSkipsDeadLetteredAndIncludesRecentlyCompleted(t *testing.T) {
	s := testStore(t)
	const acct = "alice@example.com"
	seedWithFolders(t, s, "Alice", acct)
	dead, _ := s.QueueOp(OpFlag, acct, "Inbox", FlagPayload{UIDs: []uint32{1}, Flagged: true})
	for i := 0; i < opMaxAttempts; i++ {
		if !s.StartOpNow(dead) {
			t.Fatalf("attempt %d not claimed", i+1)
		}
		s.FailOp(dead, "boom")
	}
	// The dead op last changed well before the verify pass started.
	if _, err := s.DB().Exec(`UPDATE pending_ops SET updated_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), dead); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	done, _ := s.QueueOp(OpMarkUnread, acct, "Inbox", MarkUnreadPayload{UIDs: []uint32{2}})
	s.StartOp(done)
	s.CompleteOp(done)
	if got := s.PendingUIDs(acct, "Inbox"); len(got) != 0 {
		t.Fatalf("PendingUIDs = %v, want none (dead-lettered and completed excluded)", got)
	}
	if got := s.PendingUIDsSince(acct, "Inbox", before); !got[2] || got[1] {
		t.Fatalf("PendingUIDsSince = %v, want only UID 2", got)
	}
}
