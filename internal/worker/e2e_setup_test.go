package worker_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/setup"
	"github.com/gausejakub/vimail/internal/worker"
)

type noNet struct{}

func (noNet) LookupMX(context.Context, string) ([]string, error) { return nil, errors.New("none") }
func (noNet) LookupSRV(context.Context, string, string) (string, int, error) {
	return "", 0, errors.New("none")
}
func (noNet) Fetch(context.Context, string) ([]byte, error) { return nil, errors.New("none") }
func (noNet) ProbeTLS(context.Context, string, int) bool    { return false }

// The add-account flow against real (test) servers: the connection test
// rejects a wrong password, accepts the right one, and the saved account
// then syncs.
func TestSetupAddsAWorkingAccount(t *testing.T) {
	cert, pool := testCert(t)
	t.Cleanup(worker.SetTestTLSRoots(pool))
	imapSrv := startIMAP(t, cert, pool, me)
	smtpSrv := startSMTP(t, cert)
	t.Setenv("HOME", t.TempDir())

	lines := []string{
		me,
		"n",                        // no ISPDB
		"127.0.0.1",                // IMAP host, typed by hand
		strconv.Itoa(imapSrv.port), // IMAP port
		"127.0.0.1",                // SMTP host
		strconv.Itoa(smtpSrv.port), // SMTP port
		"y",                        // use these servers
		"r",                        // retry after the wrong password
		"Jakub",                    // display name
	}
	secrets := []string{"wrong-password", testPassword}
	var out bytes.Buffer
	stored := map[string]string{}
	flow := &setup.Flow{
		In:  bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n")),
		Out: &out,
		ReadSecret: func() (string, error) {
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
		Resolver:      noNet{},
		Check:         worker.CheckAccount,
		OAuth:         func(config.AccountConfig) (*auth.Credentials, error) { return nil, errors.New("unused") },
		StorePassword: func(email, pw string) error { stored[email] = pw; return nil }, // never the real keyring
		Append:        config.AppendAccount,
	}
	acct, err := flow.Add(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("Add: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "IMAP:") || !strings.Contains(out.String(), "IMAP login OK") {
		t.Fatalf("expected a failed and then a successful test:\n%s", out.String())
	}
	if stored[me] != testPassword {
		t.Fatalf("stored = %v", stored)
	}
	cfg, err := config.Load()
	if err != nil || len(cfg.Accounts) != 1 || cfg.Accounts[0].Email != me || cfg.Accounts[0].IMAPPort != imapSrv.port {
		t.Fatalf("config = %+v, %v", cfg.Accounts, err)
	}
	// The saved settings are usable as they are.
	check := worker.CheckAccount(acct, testCreds(me))
	if !check.OK() || check.Folders["Sent"] != "Sent" || check.Folders["Inbox"] != "INBOX" {
		t.Fatalf("check = %+v", check)
	}
}
