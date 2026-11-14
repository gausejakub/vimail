package worker_test

// End-to-end harness: an in-memory IMAP server and a recording SMTP server,
// both over TLS with a throwaway certificate, on 127.0.0.1. Nothing here
// talks to a real mail server, keyring or the user's vimail config.

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/worker"
)

const testPassword = "test-password"

// testCert returns a self-signed certificate for 127.0.0.1 and a pool
// trusting it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "vimail-e2e"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// imapTestServer is an in-memory IMAP server with one user per address.
type imapTestServer struct {
	addr  string
	port  int
	pool  *x509.CertPool
	users map[string]*imapmemserver.User
}

func startIMAP(t *testing.T, cert tls.Certificate, pool *x509.CertPool, users ...string) *imapTestServer {
	t.Helper()
	mem := imapmemserver.New()
	srv := &imapTestServer{pool: pool, users: map[string]*imapmemserver.User{}}
	for _, u := range users {
		user := imapmemserver.NewUser(u, testPassword)
		for _, box := range []string{"INBOX", "Sent", "Drafts"} {
			if err := user.Create(box, nil); err != nil {
				t.Fatal(err)
			}
		}
		mem.AddUser(user)
		srv.users[u] = user
	}
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
	go server.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}}))
	srv.addr = ln.Addr().String()
	_, p, _ := net.SplitHostPort(srv.addr)
	srv.port, _ = strconv.Atoi(p)
	return srv
}

// client opens an independent IMAP session as user (the "other mail client").
func (s *imapTestServer) client(t *testing.T, user string) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialTLS(s.addr, &imapclient.Options{TLSConfig: &tls.Config{RootCAs: s.pool, ServerName: "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(user, testPassword).Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (s *imapTestServer) appendRaw(t *testing.T, user, mailbox string, raw []byte, flags ...imap.Flag) {
	t.Helper()
	c := s.client(t, user)
	cmd := c.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	if _, err := cmd.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatalf("append to %s/%s: %v", user, mailbox, err)
	}
}

type storedMessage struct {
	uid   imap.UID
	flags []imap.Flag
	raw   []byte
}

func (m storedMessage) has(flag imap.Flag) bool {
	for _, f := range m.flags {
		if strings.EqualFold(string(f), string(flag)) {
			return true
		}
	}
	return false
}

// messages returns every message in a mailbox as the server stores it.
func (s *imapTestServer) messages(t *testing.T, user, mailbox string) []storedMessage {
	t.Helper()
	c := s.client(t, user)
	data, err := c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if data.NumMessages == 0 {
		return nil
	}
	section := &imap.FetchItemBodySection{Peek: true}
	var all imap.SeqSet
	all.AddRange(1, data.NumMessages)
	bufs, err := c.Fetch(all, &imap.FetchOptions{UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var out []storedMessage
	for _, b := range bufs {
		out = append(out, storedMessage{uid: b.UID, flags: b.Flags, raw: b.FindBodySection(section)})
	}
	return out
}

// smtpSink is an SMTP server that records messages and delivers nothing.
type smtpSink struct {
	port int
	mu   sync.Mutex
	got  []smtpMessage
}

type smtpMessage struct {
	from  string
	rcpts []string
	data  []byte
}

func (s *smtpSink) messages() []smtpMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtpMessage(nil), s.got...)
}

func startSMTP(t *testing.T, cert tls.Certificate) *smtpSink {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	sink := &smtpSink{}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	sink.port, _ = strconv.Atoi(p)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go sink.serve(conn)
		}
	}()
	return sink
}

func (s *smtpSink) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(line string) { conn.Write([]byte(line + "\r\n")) }
	reply("220 vimail-e2e ESMTP")
	var cur smtpMessage
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			reply("250-vimail-e2e")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(verb, "AUTH"):
			reply("235 2.7.0 accepted")
		case strings.HasPrefix(verb, "MAIL FROM:"):
			cur = smtpMessage{from: strings.Trim(line[len("MAIL FROM:"):], "<> ")}
			reply("250 ok")
		case strings.HasPrefix(verb, "RCPT TO:"):
			cur.rcpts = append(cur.rcpts, strings.Trim(line[len("RCPT TO:"):], "<> "))
			reply("250 ok")
		case verb == "DATA":
			reply("354 end with .")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".")) // dot-unstuffing
			}
			cur.data = []byte(data.String())
			s.mu.Lock()
			s.got = append(s.got, cur)
			s.mu.Unlock()
			reply("250 queued (not delivered: test sink)")
		case verb == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func testCreds(user string) *auth.Credentials {
	return &auth.Credentials{Username: user, Password: testPassword, AuthMethod: auth.AuthPlain}
}

var _ = worker.SetTestTLSRoots

// openMemory opens a private in-memory cache database.
func openMemory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
