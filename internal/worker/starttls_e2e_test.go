package worker_test

import (
	"crypto/tls"
	"net"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/worker"
)

// All three connection modes go through vimail's own dial (which bounds the
// size of a response). STARTTLS must still upgrade before the login: this
// server refuses a login on a connection that is not encrypted.
func TestIMAPConnectsOverSTARTTLS(t *testing.T) {
	cert, pool := testCert(t)
	defer worker.SetTestTLSRoots(pool)()

	const user = "alice@example.com"
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(user, testPassword)
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve(ln) // plaintext listener: TLS only through STARTTLS

	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	acct := config.AccountConfig{Email: user, IMAPHost: "127.0.0.1", IMAPPort: port, TLS: "starttls"}
	creds := &auth.Credentials{Username: user, Password: testPassword, AuthMethod: auth.AuthPlain}

	w := worker.NewIMAPWorker(acct, creds, cache.NewSQLiteStore(db))
	if err := w.Connect(); err != nil {
		t.Fatalf("connect over STARTTLS: %v", err)
	}
	t.Cleanup(w.Disconnect)
	if !w.Ping() {
		t.Fatal("no working connection after STARTTLS")
	}

	// The same server, asked for no TLS at all: the login is refused.
	acct.TLS = "none"
	plain := worker.NewIMAPWorker(acct, creds, cache.NewSQLiteStore(db))
	if err := plain.Connect(); err == nil {
		plain.Disconnect()
		t.Fatal("login without TLS succeeded on a server that requires it: the test proves nothing")
	}
	// An unknown mode is an error, not a silent choice.
	acct.TLS = "startls"
	if err := worker.NewIMAPWorker(acct, creds, cache.NewSQLiteStore(db)).Connect(); err == nil {
		t.Fatal("a misspelt TLS mode was accepted")
	}
}
