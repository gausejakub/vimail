package worker

import (
	"errors"
	"testing"

	"github.com/gausejakub/vimail/internal/cache"
)

func testQueueStore(t *testing.T) *cache.SQLiteStore {
	t.Helper()
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return cache.NewSQLiteStore(db)
}

func opByID(ops []cache.QueuedOp, id int64) *cache.QueuedOp {
	for i := range ops {
		if ops[i].ID == id {
			return &ops[i]
		}
	}
	return nil
}

// queueMarkRead queues a mark-read op and returns its ID.
func queueMarkRead(t *testing.T, s *cache.SQLiteStore, folder string, uids ...uint32) int64 {
	t.Helper()
	id, err := s.QueueOp(cache.OpMarkRead, "alice@example.com", folder, cache.MarkReadPayload{UIDs: uids})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	return id
}

func TestRetryMarkReadBatchesMixedOutcomes(t *testing.T) {
	s := testQueueStore(t)

	inboxOp1 := queueMarkRead(t, s, "Inbox", 1)
	inboxOp2 := queueMarkRead(t, s, "Inbox", 2, 3)
	archiveOp := queueMarkRead(t, s, "Archive", 9)

	batches := map[string]*markReadBatch{
		"Inbox":   {uids: []uint32{1, 2, 3}, opIDs: []int64{inboxOp1, inboxOp2}},
		"Archive": {uids: []uint32{9}, opIDs: []int64{archiveOp}},
	}

	// Inbox batch succeeds, Archive batch fails.
	calls := make(map[string][]uint32)
	retryMarkReadBatches(s, "alice@example.com", batches, func(folder string, uids []uint32) error {
		calls[folder] = uids
		if folder == "Archive" {
			return errors.New("STORE failed")
		}
		return nil
	})

	if len(calls) != 2 {
		t.Fatalf("expected one batch call per folder, got %d", len(calls))
	}
	if got := calls["Inbox"]; len(got) != 3 {
		t.Errorf("Inbox batch UIDs = %v, want 3 UIDs", got)
	}

	pending := s.PendingOps()
	// Both Inbox ops completed — no longer retryable.
	for _, id := range []int64{inboxOp1, inboxOp2} {
		if opByID(pending, id) != nil {
			t.Errorf("op %d in succeeded folder still pending", id)
		}
	}
	// The Archive op failed and must stay retryable with the batch error.
	got := opByID(pending, archiveOp)
	if got == nil {
		t.Fatal("op in failed folder dropped from retry set")
	}
	if got.Status != cache.OpFailed || got.Error != "STORE failed" {
		t.Errorf("failed op = status %s error %q, want %s / %q", got.Status, got.Error, cache.OpFailed, "STORE failed")
	}
}

func TestRetryMarkReadBatchesAllSucceed(t *testing.T) {
	s := testQueueStore(t)

	id1 := queueMarkRead(t, s, "Inbox", 1)
	id2 := queueMarkRead(t, s, "Sent", 2)

	batches := map[string]*markReadBatch{
		"Inbox": {uids: []uint32{1}, opIDs: []int64{id1}},
		"Sent":  {uids: []uint32{2}, opIDs: []int64{id2}},
	}
	retryMarkReadBatches(s, "alice@example.com", batches, func(string, []uint32) error { return nil })

	if got := s.PendingOps(); len(got) != 0 {
		t.Errorf("expected empty retry set after success, got %d ops", len(got))
	}
}

func TestRetryMarkReadBatchesFailureThenRetrySucceeds(t *testing.T) {
	s := testQueueStore(t)

	id := queueMarkRead(t, s, "Inbox", 1)
	batches := map[string]*markReadBatch{
		"Inbox": {uids: []uint32{1}, opIDs: []int64{id}},
	}

	// First attempt fails — op stays retryable.
	retryMarkReadBatches(s, "alice@example.com", batches, func(string, []uint32) error {
		return errors.New("offline")
	})
	if opByID(s.PendingOps(), id) == nil {
		t.Fatal("failed op not retryable")
	}

	// Reconnect retry succeeds — op completes.
	retryMarkReadBatches(s, "alice@example.com", batches, func(string, []uint32) error { return nil })
	if opByID(s.PendingOps(), id) != nil {
		t.Error("op still pending after successful retry")
	}
}
