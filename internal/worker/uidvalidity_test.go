package worker

import (
	"errors"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/cache"
)

// syncedInbox returns a connected worker whose cache holds an Inbox with n
// messages on the server.
func syncedInbox(t *testing.T, n int) (*IMAPWorker, *cache.SQLiteStore, func() uint32) {
	t.Helper()
	acct, creds, user := newIMAPTestEndpoint(t)
	store := testQueueStore(t)
	if err := store.SeedAccount("Alice", acct.Email, acct.IMAPHost, acct.IMAPPort, "", 587); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureFolder(acct.Email, "Inbox"); err != nil {
		t.Fatal(err)
	}
	w := NewIMAPWorker(acct, creds, store)
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)
	for i := 0; i < n; i++ {
		raw := []byte("From: sender@example.com\r\nTo: alice@example.com\r\nSubject: test\r\n\r\nbody")
		cmd := w.client.Append("INBOX", int64(len(raw)), nil)
		if _, err := cmd.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.SyncFolder("Inbox"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	count := func() uint32 {
		status, err := user.Status("INBOX", &imap.StatusOptions{NumMessages: true})
		if err != nil || status.NumMessages == nil {
			t.Fatalf("status: %v", err)
		}
		return *status.NumMessages
	}
	return w, store, count
}

// The UIDs a write carries come from the cache. When the folder's
// UIDVALIDITY on the server is no longer the cached one, those UIDs name
// other messages: nothing may be deleted, moved or flagged, and the queued
// operations for the folder must not be replayed later.
func TestWritesRefuseUIDsOfAnotherUIDValidity(t *testing.T) {
	w, store, count := syncedInbox(t, 2)
	acct := w.acct.Email
	validity, err := store.GetUIDValidity(acct, "Inbox")
	if err != nil || validity == 0 {
		t.Fatalf("no cached UIDVALIDITY: %d, %v", validity, err)
	}
	// The cache was filled under another numbering than the server has now.
	if err := store.SetUIDValidity(acct, "Inbox", validity+1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueOp(cache.OpDelete, acct, "Inbox", cache.DeletePayload{UIDs: []uint32{1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueOp(cache.OpFlag, acct, "Inbox", cache.FlagPayload{UIDs: []uint32{2}, Flagged: true}); err != nil {
		t.Fatal(err)
	}

	if err := w.MoveToTrashBatch("Inbox", []uint32{1}, nil); !errors.Is(err, ErrUIDValidityChanged) {
		t.Fatalf("MoveToTrashBatch = %v, want ErrUIDValidityChanged", err)
	}
	if err := w.MoveToTrash("Inbox", 1); !errors.Is(err, ErrUIDValidityChanged) {
		t.Fatalf("MoveToTrash = %v, want ErrUIDValidityChanged", err)
	}
	if _, err := w.MoveToFolderBatchWithUIDs("Inbox", []uint32{1}, "Trash"); !errors.Is(err, ErrUIDValidityChanged) {
		t.Fatalf("MoveToFolderBatchWithUIDs = %v, want ErrUIDValidityChanged", err)
	}
	if err := w.setFlagBatch("Inbox", []uint32{2}, imap.FlagFlagged, true); !errors.Is(err, ErrUIDValidityChanged) {
		t.Fatalf("setFlagBatch = %v, want ErrUIDValidityChanged", err)
	}
	if n := count(); n != 2 {
		t.Fatalf("INBOX holds %d messages, want both untouched", n)
	}
	if ops := store.PendingOps(); len(ops) != 0 {
		t.Fatalf("%d queued operations would still replay: %+v", len(ops), ops)
	}
}

// A sync that finds a new UIDVALIDITY drops the operations queued under the
// old one, and a full sync does not keep the old message's body under the
// new message's header.
func TestSyncAfterUIDValidityChangeDropsOpsAndBodies(t *testing.T) {
	for _, full := range []bool{false, true} {
		w, store, _ := syncedInbox(t, 1)
		acct := w.acct.Email
		if err := store.UpdateMessageBody(acct, "Inbox", 1, "the previous message's text", "", nil); err != nil {
			t.Fatal(err)
		}
		validity, _ := store.GetUIDValidity(acct, "Inbox")
		if err := store.SetUIDValidity(acct, "Inbox", validity+1); err != nil {
			t.Fatal(err)
		}
		if _, err := store.QueueOp(cache.OpDelete, acct, "Inbox", cache.DeletePayload{UIDs: []uint32{1}}); err != nil {
			t.Fatal(err)
		}
		var err error
		if full {
			_, err = w.SyncFolderFull("Inbox")
		} else {
			_, err = w.SyncFolder("Inbox")
		}
		if err != nil {
			t.Fatalf("sync (full=%v): %v", full, err)
		}
		msg, bodyCached, ok := store.MessageByUID(acct, "Inbox", 1)
		if !ok {
			t.Fatalf("full=%v: the message is missing after the sync", full)
		}
		if bodyCached || msg.Body != "" {
			t.Errorf("full=%v: the new message shows the old body %q", full, msg.Body)
		}
		if ops := store.PendingOps(); len(ops) != 0 {
			t.Errorf("full=%v: %d operations queued under the old numbering would still replay", full, len(ops))
		}
		if got, _ := store.GetUIDValidity(acct, "Inbox"); got != validity {
			t.Errorf("full=%v: cached UIDVALIDITY = %d, want the server's %d", full, got, validity)
		}
	}
}

// Without UID EXPUNGE, deleting must not purge messages another client had
// flagged \Deleted and left in the folder.
func TestDeleteSparesMessagesOtherClientsMarkedDeleted(t *testing.T) {
	w, _, count := syncedInbox(t, 2)
	if w.client.Caps().Has(imap.CapUIDPlus) {
		t.Skip("the test server offers UID EXPUNGE")
	}
	if _, err := w.client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	var other imap.UIDSet
	other.AddNum(2)
	if err := w.client.Store(other, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.MoveToTrashBatch("Inbox", []uint32{1}, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("INBOX holds %d messages, want the one another client marked", n)
	}
	found, err := w.client.UIDSearch(&imap.SearchCriteria{Flag: []imap.Flag{imap.FlagDeleted}}, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if uids := found.AllUIDs(); len(uids) != 1 || uids[0] != 2 {
		t.Fatalf("messages still marked \\Deleted = %v, want [2]", uids)
	}
}
