package worker

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
)

func testCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	return NewCoordinator(config.Config{}, testQueueStore(t))
}

func TestSyncFolderIfIdleCoalescesBurst(t *testing.T) {
	c := testCoordinator(t)

	// A rapid burst of stale-UID responses for the same folder must yield
	// exactly one sync command; the rest coalesce into it.
	started := 0
	for i := 0; i < 5; i++ {
		if cmd := c.SyncFolderIfIdle("alice@example.com", "Inbox"); cmd != nil {
			started++
		}
	}
	if started != 1 {
		t.Errorf("burst of 5 stale UIDs started %d syncs, want exactly 1", started)
	}
}

func TestSyncFolderIfIdleConcurrentBurst(t *testing.T) {
	c := testCoordinator(t)

	var started atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cmd := c.SyncFolderIfIdle("alice@example.com", "Inbox"); cmd != nil {
				started.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := started.Load(); got != 1 {
		t.Errorf("concurrent burst started %d syncs, want exactly 1", got)
	}
}

func TestSyncFolderIfIdleClearsOnFailure(t *testing.T) {
	c := testCoordinator(t)

	cmd := c.SyncFolderIfIdle("alice@example.com", "Inbox")
	if cmd == nil {
		t.Fatal("first sync unexpectedly coalesced")
	}

	// No IMAP worker is configured, so running the cmd fails — the
	// in-flight mark must still be released so future recovery works.
	if res, ok := cmd().(SyncResult); !ok || res.Err == nil {
		t.Fatalf("expected failing SyncResult, got %#v", res)
	}

	if c.SyncFolderIfIdle("alice@example.com", "Inbox") == nil {
		t.Error("in-flight mark not cleared after failed sync")
	}
}

func TestSyncFolderIfIdleIndependentFolders(t *testing.T) {
	c := testCoordinator(t)

	if c.SyncFolderIfIdle("alice@example.com", "Inbox") == nil {
		t.Fatal("Inbox sync unexpectedly coalesced")
	}
	// A different folder and a different account are independent keys.
	if c.SyncFolderIfIdle("alice@example.com", "Archive") == nil {
		t.Error("Archive sync blocked by in-flight Inbox sync")
	}
	if c.SyncFolderIfIdle("bob@example.com", "Inbox") == nil {
		t.Error("bob's Inbox sync blocked by alice's in-flight sync")
	}
}
