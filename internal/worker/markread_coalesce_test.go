package worker

import (
	"testing"

	"github.com/gausejakub/vimail/internal/cache"
)

func TestClaimMarkReadBatchesGroupsByFolderAndAccount(t *testing.T) {
	s := testQueueStore(t)
	a1 := queueMarkRead(t, s, "Inbox", 1)
	a2 := queueMarkRead(t, s, "Inbox", 2)
	a3 := queueMarkRead(t, s, "Archive", 7)
	if _, err := s.QueueOp(cache.OpMarkRead, "bob@example.com", "Inbox", cache.MarkReadPayload{UIDs: []uint32{99}}); err != nil {
		t.Fatal(err)
	}
	all, err := s.QueueOp(cache.OpMarkRead, "alice@example.com", "Inbox", cache.MarkReadPayload{All: true})
	if err != nil {
		t.Fatal(err)
	}

	batches := claimMarkReadBatches(s, "alice@example.com")
	if len(batches) != 2 {
		t.Fatalf("got %d folder batches, want 2", len(batches))
	}
	if b := batches["Inbox"]; b == nil || len(b.uids) != 2 || b.uids[0] != 1 || b.uids[1] != 2 || len(b.opIDs) != 2 || b.opIDs[0] != a1 || b.opIDs[1] != a2 {
		t.Fatalf("Inbox batch = %+v", batches["Inbox"])
	}
	if b := batches["Archive"]; b == nil || len(b.uids) != 1 || b.opIDs[0] != a3 {
		t.Fatalf("Archive batch = %+v", batches["Archive"])
	}

	// Claimed ops are no longer pending; the other account and the
	// whole-folder op are left for their own drains.
	pending := s.PendingOps()
	for _, id := range []int64{a1, a2, a3} {
		if op := opByID(pending, id); op != nil && op.Status != cache.OpRunning {
			t.Fatalf("op %d status %s, want claimed", id, op.Status)
		}
	}
	if op := opByID(pending, all); op == nil || op.Status != cache.OpPending {
		t.Fatalf("whole-folder op should stay pending, got %+v", op)
	}
	if again := claimMarkReadBatches(s, "alice@example.com"); len(again) != 0 {
		t.Fatalf("second claim got %d batches, want 0 (already claimed)", len(again))
	}
}

func TestClaimMarkReadBatchesRespectsRetryBackoff(t *testing.T) {
	s := testQueueStore(t)
	id := queueMarkRead(t, s, "Inbox", 5)
	if !s.StartOp(id) {
		t.Fatal("initial claim failed")
	}
	s.FailOp(id, "STORE failed") // schedules a backoff
	if got := claimMarkReadBatches(s, "alice@example.com"); len(got) != 0 {
		t.Fatalf("op in backoff was claimed: %+v", got)
	}
}
