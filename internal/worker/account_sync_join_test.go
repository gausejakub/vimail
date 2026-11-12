package worker

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/config"
)

func newHookedCoordinator(t *testing.T, hook func(config.AccountConfig) error) (*Coordinator, config.AccountConfig) {
	t.Helper()
	acct := config.AccountConfig{Email: "alice@example.com", IMAPHost: "imap.example.com"}
	c := NewCoordinator(config.Config{Accounts: []config.AccountConfig{acct}}, testQueueStore(t))
	c.syncAccountHook = hook
	return c, acct
}

func TestSecondAccountSyncJoinsRunningOne(t *testing.T) {
	var runs atomic.Int32
	release := make(chan struct{})
	c, acct := newHookedCoordinator(t, func(config.AccountConfig) error {
		runs.Add(1)
		<-release
		return errors.New("boom")
	})

	first := c.startAccountSync(acct)
	second := c.startAccountSync(acct)
	if first != second {
		t.Fatal("second request started a new sync instead of joining")
	}
	close(release)
	<-second.done
	if runs.Load() != 1 {
		t.Fatalf("sync ran %d times, want 1", runs.Load())
	}
	if second.err == nil || second.err.Error() != "boom" {
		t.Fatalf("joined result err = %v, want the running sync's error", second.err)
	}

	// Once finished, a new request starts a fresh sync.
	third := c.startAccountSync(acct)
	<-third.done
	if third == first || runs.Load() != 2 {
		t.Fatalf("finished sync was reused (runs=%d)", runs.Load())
	}
}

func TestAccountSyncTimeoutContinuesInBackground(t *testing.T) {
	old := syncAccountTimeout
	syncAccountTimeout = 20 * time.Millisecond
	defer func() { syncAccountTimeout = old }()

	release := make(chan struct{})
	c, acct := newHookedCoordinator(t, func(config.AccountConfig) error {
		<-release
		return nil
	})

	msg := c.syncAccountCmd(acct)().(SyncAccountCompleteMsg)
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "continuing in the background") {
		t.Fatalf("timeout err = %v, want a continuing-in-background report", msg.Err)
	}
	// The sync is still running; a retry joins it rather than failing on the lock.
	running := c.startAccountSync(acct)
	close(release)
	select {
	case <-running.done:
	case <-time.After(time.Second):
		t.Fatal("background sync never finished")
	}
	if running.err != nil {
		t.Fatalf("background sync err = %v", running.err)
	}
}
