package worker

import (
	"errors"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
)

func TestSyncAccountNowReportsLockContention(t *testing.T) {
	store := testQueueStore(t)
	const account = "alice@example.com"
	release, ok := store.TryAcquireSyncLock(account)
	if !ok {
		t.Fatal("acquire setup sync lock")
	}
	defer release()

	coord := NewCoordinator(config.Config{Accounts: []config.AccountConfig{{
		Email: account, IMAPHost: "imap.example.com",
	}}}, store)
	if _, err := coord.SyncAccountNow(account); !errors.Is(err, ErrSyncLocked) {
		t.Fatalf("SyncAccountNow error = %v, want ErrSyncLocked", err)
	}
}

func TestSyncFolderNowReportsLockContention(t *testing.T) {
	store := testQueueStore(t)
	const account = "alice@example.com"
	release, ok := store.TryAcquireSyncLock(account)
	if !ok {
		t.Fatal("acquire setup sync lock")
	}
	defer release()

	coord := NewCoordinator(config.Config{Accounts: []config.AccountConfig{{
		Email: account, IMAPHost: "imap.example.com",
	}}}, store)
	if _, err := coord.SyncFolderNow(account, "Inbox"); !errors.Is(err, ErrSyncLocked) {
		t.Fatalf("SyncFolderNow error = %v, want ErrSyncLocked", err)
	}
}
