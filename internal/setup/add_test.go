package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/autoconfig"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/worker"
)

type fakeResolver struct {
	mx    map[string][]string
	pages map[string]string
	asked []string
}

func (f *fakeResolver) LookupMX(_ context.Context, d string) ([]string, error) { return f.mx[d], nil }
func (f *fakeResolver) LookupSRV(context.Context, string, string) (string, int, error) {
	return "", 0, errors.New("none")
}
func (f *fakeResolver) Fetch(_ context.Context, url string) ([]byte, error) {
	f.asked = append(f.asked, url)
	if p, ok := f.pages[url]; ok {
		return []byte(p), nil
	}
	return nil, errors.New("404")
}
func (f *fakeResolver) ProbeTLS(context.Context, string, int) bool { return false }

// harness scripts the terminal and records every effect of the flow.
type harness struct {
	flow     *Flow
	out      bytes.Buffer
	secrets  []string
	checks   []config.AccountConfig
	password map[string]string
	saved    []config.AccountConfig
	stored   map[string]string
}

func newHarness(t *testing.T, lines []string, secrets []string, r autoconfig.Resolver) *harness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	h := &harness{secrets: secrets, password: map[string]string{}, stored: map[string]string{}}
	h.flow = &Flow{
		In:  bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n")),
		Out: &h.out,
		ReadSecret: func() (string, error) {
			if len(h.secrets) == 0 {
				return "", errors.New("no more secrets scripted")
			}
			s := h.secrets[0]
			h.secrets = h.secrets[1:]
			return s, nil
		},
		Resolver: r,
		Check: func(a config.AccountConfig, c *auth.Credentials) worker.AccountCheck {
			h.checks = append(h.checks, a)
			if want, ok := h.password[a.IMAPHost]; ok && c.Password != want {
				return worker.AccountCheck{IMAPErr: errors.New("authentication failed"), SMTPErr: errors.New("authentication failed")}
			}
			return worker.AccountCheck{Folders: map[string]string{"Inbox": "INBOX", "Sent": "Sent", "Drafts": "Drafts", "Trash": "Trash"}}
		},
		OAuth: func(config.AccountConfig) (*auth.Credentials, error) {
			return nil, errors.New("OAuth not scripted")
		},
		StorePassword: func(email, pw string) error { h.stored[email] = pw; return nil },
		Append:        func(a config.AccountConfig) error { h.saved = append(h.saved, a); return config.AppendAccount(a) },
	}
	return h
}

func TestAddAccountFromAddressAlone(t *testing.T) {
	r := &fakeResolver{mx: map[string][]string{"topol.test": {"in1-smtp.messagingengine.com"}}}
	h := newHarness(t, []string{"Jakub@Topol.test", "" /* use servers */, "Jakub Gause"}, []string{"s3cr3t-token"}, r)
	acct, err := h.flow.Add(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("Add: %v\n%s", err, h.out.String())
	}
	if acct.Email != "jakub@topol.test" || acct.IMAPHost != "imap.fastmail.com" || acct.SMTPPort != 465 || acct.AuthMethod != "app-password" || acct.Name != "Jakub Gause" {
		t.Fatalf("account = %+v", acct)
	}
	if h.stored["jakub@topol.test"] != "s3cr3t-token" {
		t.Fatalf("password not stored: %v", h.stored)
	}
	// The written config loads back as the same account.
	cfg, err := config.Load()
	if err != nil || len(cfg.Accounts) != 1 || cfg.Accounts[0].IMAPHost != "imap.fastmail.com" || cfg.Accounts[0].Email != "jakub@topol.test" {
		t.Fatalf("config after setup: %+v %v", cfg.Accounts, err)
	}
	if info, _ := os.Stat(config.Path()); info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(config.Path())
	if strings.Contains(string(raw), "s3cr3t-token") {
		t.Fatal("password written to the config file")
	}
	if !strings.Contains(h.out.String(), "Fastmail") || !strings.Contains(h.out.String(), "imap.fastmail.com:993") {
		t.Fatalf("servers not shown before use:\n%s", h.out.String())
	}
}

// The password goes nowhere until the user has seen and accepted the servers.
func TestNoCredentialsBeforeConfirmation(t *testing.T) {
	r := &fakeResolver{pages: map[string]string{"https://autoconfig.evil.test/mail/config-v1.1.xml": `<clientConfig><emailProvider>
		<incomingServer type="imap"><hostname>imap.attacker.test</hostname><port>993</port><socketType>SSL</socketType></incomingServer>
		<outgoingServer type="smtp"><hostname>smtp.attacker.test</hostname><port>465</port><socketType>SSL</socketType></outgoingServer>
	</emailProvider></clientConfig>`}}
	h := newHarness(t, []string{"me@evil.test", "n"}, []string{"secret"}, r)
	if _, err := h.flow.Add(context.Background(), config.Config{}); err == nil {
		t.Fatal("declined setup reported success")
	}
	if len(h.checks) != 0 || len(h.stored) != 0 || len(h.saved) != 0 || len(h.secrets) != 1 {
		t.Fatalf("effects after declining: checks=%d stored=%d saved=%d secretsLeft=%d", len(h.checks), len(h.stored), len(h.saved), len(h.secrets))
	}
	if !strings.Contains(h.out.String(), "imap.attacker.test:993") {
		t.Fatalf("detected host not shown:\n%s", h.out.String())
	}
	if _, err := os.Stat(config.Path()); !os.IsNotExist(err) {
		t.Fatal("config written although setup was declined")
	}
}

func TestWrongPasswordRetriesAndSavesNothingUntilItWorks(t *testing.T) {
	r := &fakeResolver{mx: map[string][]string{"gause.test": {"mx1.seznam.cz"}}}
	h := newHarness(t, []string{"jakub@gause.test", "y", "r", ""}, []string{"wrong", "right"}, r)
	h.password["imap.seznam.cz"] = "right"
	if _, err := h.flow.Add(context.Background(), config.Config{}); err != nil {
		t.Fatalf("Add: %v\n%s", err, h.out.String())
	}
	if len(h.checks) != 2 || h.stored["jakub@gause.test"] != "right" || len(h.saved) != 1 {
		t.Fatalf("checks=%d stored=%v saved=%d", len(h.checks), h.stored, len(h.saved))
	}

	// Giving up after a failed login saves nothing.
	h = newHarness(t, []string{"jakub@gause.test", "y", "q"}, []string{"wrong"}, r)
	h.password["imap.seznam.cz"] = "right"
	if _, err := h.flow.Add(context.Background(), config.Config{}); err == nil {
		t.Fatal("quit reported success")
	}
	if len(h.stored)+len(h.saved) != 0 {
		t.Fatalf("saved after quitting: %v %v", h.stored, h.saved)
	}
}

func TestManualEntryISPDBConsentAndDuplicates(t *testing.T) {
	r := &fakeResolver{}
	// Unknown domain: ISPDB declined, servers typed by hand.
	h := newHarness(t, []string{"a@unknown.test", "n", "mail.unknown.test", "", "mail.unknown.test", "587", "y", ""}, []string{"pw"}, r)
	acct, err := h.flow.Add(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("Add: %v\n%s", err, h.out.String())
	}
	if acct.IMAPHost != "mail.unknown.test" || acct.IMAPPort != 993 || acct.SMTPPort != 587 {
		t.Fatalf("account = %+v", acct)
	}
	for _, url := range r.asked {
		if strings.Contains(url, "thunderbird.net") {
			t.Fatalf("ISPDB asked after the user said no: %s", url)
		}
	}
	// Adding the same address again is refused; a second account appends.
	cfg, _ := config.Load()
	h2 := newHarness(t, []string{"A@unknown.test"}, nil, r)
	if _, err := h2.flow.Add(context.Background(), cfg); err == nil {
		t.Fatal("duplicate address accepted")
	}
	for _, bad := range []string{"not-an-address", "a@b", "a@b.test\x1b[2J"} {
		h3 := newHarness(t, []string{bad}, nil, r)
		if _, err := h3.flow.Add(context.Background(), config.Config{}); err == nil && bad != "a@b.test\x1b[2J" {
			t.Errorf("address %q accepted", bad)
		}
	}
}

func TestAppendAccountKeepsExistingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := config.Path()
	os.MkdirAll(filepath.Dir(path), 0700)
	existing := "# my config\n[theme]\nname = \"nord\"   # keep this comment\n\n[[accounts]]\nemail = \"first@x.test\"\nimap_host = \"imap.x.test\"\n\n[mcp]\nallow_write = true"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	second := config.AccountConfig{Name: `Quote "Me" \ Ok`, Email: "second@y.test", IMAPHost: "imap.y.test", IMAPPort: 993, SMTPHost: "smtp.y.test", SMTPPort: 465, TLS: "tls", AuthMethod: "plain"}
	if err := config.AppendAccount(second); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), existing) {
		t.Fatalf("existing config changed:\n%s", raw)
	}
	cfg, err := config.Load()
	if err != nil || len(cfg.Accounts) != 2 || cfg.Accounts[1].Name != second.Name || cfg.Accounts[1].SMTPPort != 465 ||
		cfg.Theme.Name != "nord" || !cfg.MCP.AllowWrite {
		t.Fatalf("loaded: %+v %v", cfg, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if err := config.AppendAccount(config.AccountConfig{Email: "x@y.test", IMAPHost: "a\nb = \"c\""}); err == nil {
		t.Fatal("control character written into the config")
	}
	// A symlinked config is not replaced.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.MkdirAll(filepath.Join(dir, ".config", "vimail"), 0700)
	target := filepath.Join(dir, "managed.toml")
	os.WriteFile(target, []byte(""), 0600)
	if err := os.Symlink(target, config.Path()); err != nil {
		t.Fatal(err)
	}
	if err := config.AppendAccount(second); err == nil {
		t.Fatal("symlinked config replaced")
	}
}
