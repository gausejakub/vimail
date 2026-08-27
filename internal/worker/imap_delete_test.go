package worker

import (
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

type delayedCopySession struct {
	imapserver.Session
	delay time.Duration
}

func (s *delayedCopySession) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	time.Sleep(s.delay)
	return s.Session.Copy(numSet, dest)
}

func newIMAPTestEndpoint(t *testing.T) (config.AccountConfig, *auth.Credentials, *imapmemserver.User) {
	return newIMAPTestEndpointWithCopyDelay(t, 0)
}

func newIMAPTestEndpointWithCopyDelay(t *testing.T, copyDelay time.Duration) (config.AccountConfig, *auth.Credentials, *imapmemserver.User) {
	t.Helper()
	const (
		username = "alice@example.com"
		password = "secret"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	if err := user.Create("Deleted Items", nil); err != nil {
		t.Fatalf("create trash: %v", err)
	}
	memServer.AddUser(user)

	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			session := imapserver.Session(memServer.NewSession())
			if copyDelay > 0 {
				session = &delayedCopySession{Session: session, delay: copyDelay}
			}
			return session, nil, nil
		},
		InsecureAuth: true,
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { server.Close() })
	go func() {
		_ = server.Serve(listener)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	acct := config.AccountConfig{
		Email: username, IMAPHost: host, IMAPPort: port, TLS: "none",
	}
	creds := &auth.Credentials{
		Username: username, Password: password, AuthMethod: auth.AuthPlain,
	}
	return acct, creds, user
}

func TestMoveToTrashBatchRefreshesDeadlinePerChunk(t *testing.T) {
	acct, creds, _ := newIMAPTestEndpointWithCopyDelay(t, 60*time.Millisecond)
	w := NewIMAPWorker(acct, creds, testQueueStore(t))
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)
	w.commandDeadline = 100 * time.Millisecond

	raw := []byte("From: sender@example.com\r\nTo: alice@example.com\r\nSubject: test\r\n\r\nbody")
	appendCmd := w.client.Append("INBOX", int64(len(raw)), nil)
	if _, err := appendCmd.Write(raw); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("append wait: %v", err)
	}

	// Duplicate UID values are intentional: 501 inputs force two worker chunks
	// while requiring only one fixture message. Each COPY finishes within the
	// command deadline, but both together exceed it.
	uids := make([]uint32, imapUIDChunkSize+1)
	for i := range uids {
		uids[i] = 1
	}
	if err := w.MoveToTrashBatch("Inbox", uids, nil); err != nil {
		t.Fatalf("batch should refresh its deadline after each chunk: %v", err)
	}
}

func TestMoveToTrashDiscoversServerMailboxBeforeFirstWrite(t *testing.T) {
	acct, creds, user := newIMAPTestEndpoint(t)
	w := NewIMAPWorker(acct, creds, testQueueStore(t))
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)

	raw := []byte("From: sender@example.com\r\nTo: alice@example.com\r\nSubject: test\r\n\r\nbody")
	appendCmd := w.client.Append("INBOX", int64(len(raw)), nil)
	if _, err := appendCmd.Write(raw); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("append wait: %v", err)
	}

	if err := w.MoveToTrashBatch("Inbox", []uint32{1}, nil); err != nil {
		t.Fatalf("first write should use discovered trash mailbox: %v", err)
	}
	status, err := user.Status("Deleted Items", &imap.StatusOptions{NumMessages: true})
	if err != nil {
		t.Fatalf("trash status: %v", err)
	}
	if status.NumMessages == nil || *status.NumMessages != 1 {
		t.Fatalf("trash message count = %v, want 1", status.NumMessages)
	}
}

func TestEnsureIMAPWorkerForWriteReconnectsClosedConnection(t *testing.T) {
	acct, creds, _ := newIMAPTestEndpoint(t)
	store := testQueueStore(t)
	stale := NewIMAPWorker(acct, creds, store)
	if err := stale.Connect(); err != nil {
		t.Fatalf("connect stale worker: %v", err)
	}
	if err := stale.client.Close(); err != nil {
		t.Fatalf("close stale connection: %v", err)
	}

	c := NewCoordinator(config.Config{Accounts: []config.AccountConfig{acct}}, store)
	c.creds[acct.Email] = creds
	c.imap[acct.Email] = stale
	t.Cleanup(c.DisconnectAll)

	got, err := c.ensureIMAPWorkerForWrite(acct)
	if err != nil {
		t.Fatalf("ensure write worker: %v", err)
	}
	if got == stale {
		t.Fatal("write path reused a closed IMAP worker")
	}
	if !got.Ping() {
		t.Fatal("replacement IMAP worker is not healthy")
	}
}

func TestRestoreUsesDestinationUIDBelowCacheHighWatermark(t *testing.T) {
	acct, creds, user := newIMAPTestEndpoint(t)
	store := testQueueStore(t)
	if err := store.SeedAccount("Alice", acct.Email, acct.IMAPHost, acct.IMAPPort, "", 587); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"Inbox", "Trash"} {
		if _, err := store.EnsureFolder(acct.Email, folder); err != nil {
			t.Fatal(err)
		}
	}
	w := NewIMAPWorker(acct, creds, store)
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)

	raw := []byte("Message-ID: <restore@example.com>\r\nFrom: sender@example.com\r\nTo: alice@example.com\r\nSubject: restore\r\n\r\nbody")
	appendCmd := w.client.Append("Deleted Items", int64(len(raw)), nil)
	if _, err := appendCmd.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessage(acct.Email, "Trash", email.Message{
		UID: 1, MessageID: "<restore@example.com>", From: "sender@example.com",
		To: acct.Email, Subject: "restore", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// An incremental sync would start above this stale high-water mark and
	// miss the server-assigned destination UID 1.
	if err := store.UpsertMessage(acct.Email, "Inbox", email.Message{
		UID: 100, MessageID: "<stale@example.com>", From: "old@example.com",
		To: acct.Email, Subject: "stale", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	coord := NewCoordinator(config.Config{Accounts: []config.AccountConfig{acct}}, store)
	coord.creds[acct.Email] = creds
	coord.imap[acct.Email] = w
	result := coord.executeRestore(w, acct.Email, []uint32{1}, "Inbox")
	if result.Err != nil || !result.Delivered || !result.Cached || result.Count != 1 {
		t.Fatalf("restore result = %+v", result)
	}
	if _, _, ok := store.MessageByUID(acct.Email, "Trash", 1); ok {
		t.Fatal("restored message remains in Trash cache")
	}
	restored, _, ok := store.MessageByUID(acct.Email, "Inbox", 1)
	if !ok || restored.Subject != "restore" {
		t.Fatalf("restored Inbox message = %+v found=%v", restored, ok)
	}
	for folder, want := range map[string]uint32{"Deleted Items": 0, "INBOX": 1} {
		status, err := user.Status(folder, &imap.StatusOptions{NumMessages: true})
		if err != nil {
			t.Fatal(err)
		}
		if status.NumMessages == nil || *status.NumMessages != want {
			t.Fatalf("%s message count = %v, want %d", folder, status.NumMessages, want)
		}
	}
}

func TestSyncFolderFullReplacesStaleCacheSnapshot(t *testing.T) {
	acct, creds, _ := newIMAPTestEndpoint(t)
	store := testQueueStore(t)
	if err := store.SeedAccount("Alice", acct.Email, acct.IMAPHost, acct.IMAPPort, "", 587); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureFolder(acct.Email, "Inbox"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessage(acct.Email, "Inbox", email.Message{
		UID: 100, MessageID: "<stale@example.com>", From: "old@example.com",
		To: acct.Email, Subject: "stale", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	w := NewIMAPWorker(acct, creds, store)
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)
	raw := []byte("Message-ID: <current@example.com>\r\nFrom: sender@example.com\r\nTo: alice@example.com\r\nSubject: current\r\n\r\nbody")
	appendCmd := w.client.Append("INBOX", int64(len(raw)), nil)
	if _, err := appendCmd.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatal(err)
	}

	got, err := w.SyncFolderFull("Inbox")
	if err != nil {
		t.Fatalf("full sync: %v", err)
	}
	if got != 1 {
		t.Fatalf("full sync fetched %d messages, want 1", got)
	}
	if _, _, ok := store.MessageByUID(acct.Email, "Inbox", 100); ok {
		t.Fatal("stale cache UID survived full sync")
	}
	msg, _, ok := store.MessageByUID(acct.Email, "Inbox", 1)
	if !ok || msg.Subject != "current" {
		t.Fatalf("current message = %+v found=%v", msg, ok)
	}
}

func TestRemainingRestoreUIDsUsesConfirmedSources(t *testing.T) {
	got := remainingRestoreUIDs([]uint32{10, 20, 30}, []UIDMove{{Source: 20, Destination: 2}})
	if len(got) != 2 || got[0] != 10 || got[1] != 30 {
		t.Fatalf("remaining UIDs = %v, want [10 30]", got)
	}
}

func TestMarkAllReadCoversMessagesMissingFromCache(t *testing.T) {
	acct, creds, _ := newIMAPTestEndpoint(t)
	w := NewIMAPWorker(acct, creds, testQueueStore(t))
	if err := w.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(w.Disconnect)
	for i := 0; i < 2; i++ {
		raw := []byte("From: sender@example.com\r\nTo: alice@example.com\r\nSubject: unread\r\n\r\nbody")
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
	if err := w.MarkAllRead("Inbox"); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if _, err := w.client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	var all imap.UIDSet
	all.AddRange(1, imap.UID(math.MaxUint32))
	fetch := w.client.Fetch(all, &imap.FetchOptions{UID: true, Flags: true})
	seen := 0
	for {
		data := fetch.Next()
		if data == nil {
			break
		}
		message, err := data.Collect()
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range message.Flags {
			if flag == imap.FlagSeen {
				seen++
				break
			}
		}
	}
	if err := fetch.Close(); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("seen server messages = %d, want 2", seen)
	}
}
