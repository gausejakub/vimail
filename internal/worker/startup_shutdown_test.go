package worker

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/config"
)

// Password lookups run for all accounts at once: with a password manager each
// one is a separate command, and they used to run one after another before
// the first frame was drawn.
func TestResolveCredentialsRunsAccountsInParallel(t *testing.T) {
	var accounts []config.AccountConfig
	for _, name := range []string{"a", "b", "c", "d"} {
		accounts = append(accounts, config.AccountConfig{
			Email: name + "@example.com", AuthMethod: "plain", PasswordCmd: "sleep 0.4; echo secret-" + name,
		})
	}
	c := NewCoordinator(config.Config{Accounts: accounts}, testQueueStore(t))

	start := time.Now()
	if errs := c.ResolveCredentials(); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if d := time.Since(start); d > 1200*time.Millisecond {
		t.Fatalf("four 0.4s lookups took %v; they should overlap", d)
	}
	for _, acct := range accounts {
		creds := c.cachedCredentials(acct.Email)
		if creds == nil || creds.Password != "secret-"+acct.Email[:1] {
			t.Fatalf("%s: credentials = %+v", acct.Email, creds)
		}
	}
}

// A failing lookup for one account must not affect the others.
func TestResolveCredentialsReportsEachFailure(t *testing.T) {
	c := NewCoordinator(config.Config{Accounts: []config.AccountConfig{
		{Email: "ok@example.com", AuthMethod: "plain", PasswordCmd: "echo fine"},
		{Email: "bad@example.com", AuthMethod: "plain", PasswordCmd: "exit 3"},
	}}, testQueueStore(t))
	errs := c.ResolveCredentials()
	if len(errs) != 1 || c.cachedCredentials("ok@example.com") == nil || c.cachedCredentials("bad@example.com") != nil {
		t.Fatalf("errs=%v", errs)
	}
}

// Quitting must not wait for a server that is slow to acknowledge the logout
// (or a connection busy with an operation).
func TestDisconnectAllDoesNotWaitForSlowServers(t *testing.T) {
	c := NewCoordinator(config.Config{}, testQueueStore(t))
	busy := &IMAPWorker{acct: config.AccountConfig{Email: "slow@example.com"}}
	busy.opMu.Lock() // an operation in flight: Disconnect would wait up to disconnectWait
	defer busy.opMu.Unlock()
	c.imap["slow@example.com"] = busy
	c.imap["idle@example.com"] = &IMAPWorker{acct: config.AccountConfig{Email: "idle@example.com"}}

	start := time.Now()
	c.DisconnectAll()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("DisconnectAll took %v, want about %v", d, disconnectAllWait)
	}
	if len(c.imap) != 0 {
		t.Fatalf("workers still registered: %d", len(c.imap))
	}
}
