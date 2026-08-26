package cache

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// twoStores opens the same cache file twice, simulating the TUI and the MCP
// server sharing one database from separate processes.
func twoStores(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.db")
	dbA, err := Open(path)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { dbA.Close() })
	dbB, err := Open(path)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { dbB.Close() })
	return NewSQLiteStore(dbA), NewSQLiteStore(dbB)
}

func TestTwoDrainersExecuteEachOpExactlyOnce(t *testing.T) {
	storeA, storeB := twoStores(t)

	const numOps = 20
	ids := make([]int64, numOps)
	for i := range ids {
		id, err := storeA.QueueOp(OpSend, "alice@example.com", "", SendPayload{
			From: "a@x.com", To: "b@x.com", Subject: "s", Body: "b",
		})
		if err != nil {
			t.Fatalf("QueueOp: %v", err)
		}
		ids[i] = id
	}

	var mu sync.Mutex
	execCount := make(map[int64]int)
	drain := func(s *SQLiteStore) {
		for _, op := range s.PendingOps() {
			if !s.StartOp(op.ID) {
				continue // lost the claim race — the other drainer owns it
			}
			mu.Lock()
			execCount[op.ID]++
			mu.Unlock()
			s.CompleteOp(op.ID)
		}
	}

	var wg sync.WaitGroup
	for _, s := range []*SQLiteStore{storeA, storeB} {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			drain(s)
		}()
	}
	wg.Wait()

	for _, id := range ids {
		if got := execCount[id]; got != 1 {
			t.Errorf("op %d executed %d times, want exactly 1", id, got)
		}
	}
	if remaining := storeA.PendingOps(); len(remaining) != 0 {
		t.Errorf("%d ops left unexecuted", len(remaining))
	}
}

func TestLiveLeaseIsNeverStolen(t *testing.T) {
	storeA, storeB := twoStores(t)

	id, err := storeA.QueueOp(OpSend, "alice@example.com", "", SendPayload{To: "b@x.com"})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	if !storeA.StartOp(id) {
		t.Fatal("A's claim of pending op failed")
	}

	// B must neither see the op as retryable nor be able to claim it.
	if findOp(storeB.PendingOps(), id) != nil {
		t.Error("op with live lease visible to second drainer")
	}
	if storeB.StartOp(id) {
		t.Error("second drainer stole an op with a live lease")
	}
}

func TestExpiredLeaseIsReclaimable(t *testing.T) {
	storeA, storeB := twoStores(t)

	id, err := storeA.QueueOp(OpDelete, "alice@example.com", "Inbox", DeletePayload{UIDs: []uint32{1}})
	if err != nil {
		t.Fatalf("QueueOp: %v", err)
	}
	if !storeA.StartOp(id) {
		t.Fatal("A's claim failed")
	}

	// Simulate A crashing: its lease expires without Complete/Fail.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := storeA.db.Exec(`UPDATE pending_ops SET lease_until = ? WHERE id = ?`, past, id); err != nil {
		t.Fatalf("backdate lease: %v", err)
	}

	if findOp(storeB.PendingOps(), id) == nil {
		t.Error("expired-lease op not visible for reclaim")
	}
	if !storeB.StartOp(id) {
		t.Fatal("reclaim of expired-lease op failed")
	}

	// A comes back late and tries to complete the op it no longer owns —
	// that must not stomp B's claim.
	storeA.CompleteOp(id)
	got := findOp(storeB.RecentOps(10), id)
	if got == nil {
		t.Fatal("op missing from RecentOps")
	}
	if got.Status != OpRunning {
		t.Errorf("status after late CompleteOp by old owner = %s, want %s (B's claim intact)", got.Status, OpRunning)
	}

	// B, the current owner, can complete it.
	storeB.CompleteOp(id)
	if got := findOp(storeB.RecentOps(10), id); got == nil || got.Status != OpCompleted {
		t.Error("current owner failed to complete reclaimed op")
	}
}

func TestSyncLockSerializesAcrossProcesses(t *testing.T) {
	storeA, storeB := twoStores(t)

	releaseA, ok := storeA.TryAcquireSyncLock("alice@example.com")
	if !ok {
		t.Fatal("A failed to acquire free lock")
	}
	if _, ok := storeB.TryAcquireSyncLock("alice@example.com"); ok {
		t.Error("B acquired a lock A already holds")
	}
	// A different account is an independent lock.
	if releaseOther, ok := storeB.TryAcquireSyncLock("bob@example.com"); !ok {
		t.Error("B blocked on an unrelated account's lock")
	} else {
		releaseOther()
	}

	releaseA()
	releaseB, ok := storeB.TryAcquireSyncLock("alice@example.com")
	if !ok {
		t.Error("B failed to acquire released lock")
	} else {
		releaseB()
	}
}

func TestSyncLockExpiredHolderIsStolen(t *testing.T) {
	storeA, storeB := twoStores(t)

	if _, ok := storeA.TryAcquireSyncLock("alice@example.com"); !ok {
		t.Fatal("A failed to acquire free lock")
	}
	// A crashes without releasing; its lock expires.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := storeA.db.Exec(`UPDATE sync_locks SET expires_at = ?`, past); err != nil {
		t.Fatalf("backdate lock: %v", err)
	}

	release, ok := storeB.TryAcquireSyncLock("alice@example.com")
	if !ok {
		t.Fatal("B failed to steal expired lock")
	}
	release()
}

func TestMigratePendingDeletesConcurrentStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	dbA, err := Open(path)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	defer dbA.Close()
	dbB, err := Open(path)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	defer dbB.Close()

	// Seed legacy pending_deletes rows after both opens (the opens already
	// ran the migration against an empty table).
	seed := NewSQLiteStore(dbA)
	if err := seed.SeedAccount("Alice", "alice@example.com", "", 993, "", 587); err != nil {
		t.Fatalf("SeedAccount: %v", err)
	}
	folderID, err := seed.EnsureFolder("alice@example.com", "Inbox")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	for _, uid := range []uint32{1, 2, 3} {
		if _, err := dbA.Exec(`INSERT INTO pending_deletes (folder_id, uid, account, folder) VALUES (?, ?, 'alice@example.com', 'Inbox')`, folderID, uid); err != nil {
			t.Fatalf("seed pending_deletes: %v", err)
		}
	}

	// Two processes race the migration at startup.
	var wg sync.WaitGroup
	for _, db := range []*sql.DB{dbA, dbB} {
		db := db
		wg.Add(1)
		go func() {
			defer wg.Done()
			migratePendingDeletes(db)
		}()
	}
	wg.Wait()

	var opCount int
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM pending_ops WHERE type = ?`, string(OpDelete)).Scan(&opCount); err != nil {
		t.Fatalf("count ops: %v", err)
	}
	if opCount != 1 {
		t.Errorf("concurrent startup produced %d delete ops, want exactly 1", opCount)
	}
	var leftover int
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM pending_deletes`).Scan(&leftover); err != nil {
		t.Fatalf("count pending_deletes: %v", err)
	}
	if leftover != 0 {
		t.Errorf("%d pending_deletes rows left after migration", leftover)
	}
}
