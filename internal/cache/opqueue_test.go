package cache

import (
	"testing"
	"time"
)

// findOp returns the op with the given ID from ops, or nil.
func findOp(ops []QueuedOp, id int64) *QueuedOp {
	for i := range ops {
		if ops[i].ID == id {
			return &ops[i]
		}
	}
	return nil
}

func TestFailedOpsRemainRetryable(t *testing.T) {
	s := testStore(t)

	tests := []struct {
		opType  OpType
		payload interface{}
	}{
		{OpDelete, DeletePayload{UIDs: []uint32{1, 2}}},
		{OpSend, SendPayload{From: "a@x.com", To: "b@x.com", Subject: "hi", Body: "text"}},
		{OpMarkRead, MarkReadPayload{UIDs: []uint32{3}}},
	}

	for _, tc := range tests {
		id, err := s.QueueOp(tc.opType, "alice@example.com", "Inbox", tc.payload)
		if err != nil {
			t.Fatalf("QueueOp(%s): %v", tc.opType, err)
		}

		// pending → visible
		if findOp(s.PendingOps(), id) == nil {
			t.Errorf("%s: pending op missing from PendingOps", tc.opType)
		}

		// running with a live lease → owned by this drainer, hidden from
		// the retry set so a second drainer cannot re-pick it
		if !s.StartOp(id) {
			t.Fatalf("%s: claim of pending op failed", tc.opType)
		}
		if findOp(s.RetryableOps(), id) != nil {
			t.Errorf("%s: claimed running op still visible in PendingOps", tc.opType)
		}

		// failed → retained with backoff, then visible for reconnect retry
		s.FailOp(id, "connection lost")
		if findOp(s.RetryableOps(), id) != nil {
			t.Fatalf("%s: failed op became retryable before its backoff elapsed", tc.opType)
		}
		got := findOp(s.RecentOps(100), id)
		if got == nil {
			t.Fatalf("%s: failed op missing from queue history", tc.opType)
		}
		if got.Status != OpFailed {
			t.Errorf("%s: status = %s, want %s", tc.opType, got.Status, OpFailed)
		}
		if got.Error != "connection lost" {
			t.Errorf("%s: error = %q, want %q", tc.opType, got.Error, "connection lost")
		}
		if got.Attempts != 1 || got.NextAttemptAt.IsZero() {
			t.Errorf("%s: attempts=%d next=%v, want attempt 1 with retry time", tc.opType, got.Attempts, got.NextAttemptAt)
		}
		past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
		if _, err := s.db.Exec(`UPDATE pending_ops SET next_attempt_at = ? WHERE id = ?`, past, id); err != nil {
			t.Fatalf("%s: make retry due: %v", tc.opType, err)
		}
		if findOp(s.RetryableOps(), id) == nil {
			t.Fatalf("%s: failed op missing after backoff elapsed", tc.opType)
		}

		// completed → gone from retry set
		s.CompleteOp(id)
		if findOp(s.PendingOps(), id) != nil {
			t.Errorf("%s: completed op still in PendingOps", tc.opType)
		}
	}
}

func TestFailedOpStopsRetryingAtAttemptCap(t *testing.T) {
	s := testStore(t)
	id, err := s.QueueOp(OpMarkRead, "alice@example.com", "Inbox", MarkReadPayload{UIDs: []uint32{7}})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}

	for attempt := 1; attempt <= opMaxAttempts; attempt++ {
		if !s.StartOp(id) {
			t.Fatalf("attempt %d: claim failed", attempt)
		}
		s.FailOp(id, "still offline")
		if attempt < opMaxAttempts {
			past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
			if _, err := s.db.Exec(`UPDATE pending_ops SET next_attempt_at = ? WHERE id = ?`, past, id); err != nil {
				t.Fatalf("attempt %d: make retry due: %v", attempt, err)
			}
		}
	}

	if findOp(s.PendingOps(), id) != nil {
		t.Fatal("attempt-capped op remains eligible for retry")
	}
	got := findOp(s.RecentOps(10), id)
	if got == nil || got.Status != OpFailed || got.Attempts != opMaxAttempts {
		t.Fatalf("capped op = %+v, want failed with %d attempts", got, opMaxAttempts)
	}
}

func TestStartOpNowBypassesRetryBackoff(t *testing.T) {
	s := testStore(t)
	id, err := s.QueueOp(OpMarkRead, "alice@example.com", "Inbox", MarkReadPayload{UIDs: []uint32{7}})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	if !s.StartOp(id) {
		t.Fatal("initial claim failed")
	}
	s.FailOp(id, "offline")
	if findOp(s.RetryableOps(), id) != nil {
		t.Fatal("failed op is due before backoff elapsed")
	}
	if !s.StartOpNow(id) {
		t.Fatal("explicit retry did not bypass backoff")
	}
}

func TestRecentOpsExposesStatusAndError(t *testing.T) {
	s := testStore(t)

	id, err := s.QueueOp(OpMarkRead, "alice@example.com", "Inbox", MarkReadPayload{UIDs: []uint32{7}})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	s.StartOp(id)
	s.FailOp(id, "server said no")

	got := findOp(s.RecentOps(10), id)
	if got == nil {
		t.Fatal("failed op missing from RecentOps")
	}
	if got.Status != OpFailed || got.Error != "server said no" {
		t.Errorf("RecentOps op = status %s error %q, want %s / %q", got.Status, got.Error, OpFailed, "server said no")
	}
}

func TestCleanupOldOpsAgesOutFailed(t *testing.T) {
	s := testStore(t)

	id, err := s.QueueOp(OpDelete, "alice@example.com", "Inbox", DeletePayload{UIDs: []uint32{1}})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	s.FailOp(id, "boom")

	// Backdate the op so it exceeds the retention window.
	old := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE pending_ops SET updated_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	s.CleanupOldOps(24 * time.Hour)
	if findOp(s.PendingOps(), id) != nil {
		t.Error("aged-out failed op still in PendingOps")
	}
	if findOp(s.RecentOps(10), id) != nil {
		t.Error("aged-out failed op still in RecentOps")
	}
}
