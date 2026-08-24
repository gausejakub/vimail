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

		// running → still visible (crash recovery)
		s.StartOp(id)
		if findOp(s.PendingOps(), id) == nil {
			t.Errorf("%s: running op missing from PendingOps", tc.opType)
		}

		// failed → must stay visible so a reconnect retry can pick it up
		s.FailOp(id, "connection lost")
		got := findOp(s.PendingOps(), id)
		if got == nil {
			t.Fatalf("%s: failed op missing from PendingOps — not retryable", tc.opType)
		}
		if got.Status != OpFailed {
			t.Errorf("%s: status = %s, want %s", tc.opType, got.Status, OpFailed)
		}
		if got.Error != "connection lost" {
			t.Errorf("%s: error = %q, want %q", tc.opType, got.Error, "connection lost")
		}

		// completed → gone from retry set
		s.CompleteOp(id)
		if findOp(s.PendingOps(), id) != nil {
			t.Errorf("%s: completed op still in PendingOps", tc.opType)
		}
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
