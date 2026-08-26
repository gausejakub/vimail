package worker

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/config"
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
