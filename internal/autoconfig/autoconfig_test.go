package autoconfig

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeNet answers lookups from maps and records what was asked.
type fakeNet struct {
	mx      map[string][]string
	srv     map[string]string // "service domain" -> "host:port"
	pages   map[string]string
	tls     map[string]bool // "host:port"
	fetched []string
	probed  []string
}

func (f *fakeNet) LookupMX(_ context.Context, domain string) ([]string, error) {
	if v, ok := f.mx[domain]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no MX")
}

func (f *fakeNet) LookupSRV(_ context.Context, service, domain string) (string, int, error) {
	if v, ok := f.srv[service+" "+domain]; ok {
		var host string
		var port int
		fmt.Sscanf(strings.Replace(v, ":", " ", 1), "%s %d", &host, &port)
		return host, port, nil
	}
	return "", 0, fmt.Errorf("no SRV")
}

func (f *fakeNet) Fetch(_ context.Context, url string) ([]byte, error) {
	f.fetched = append(f.fetched, url)
	if v, ok := f.pages[url]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("404")
}

func (f *fakeNet) ProbeTLS(_ context.Context, host string, port int) bool {
	key := JoinHostPort(host, port)
	f.probed = append(f.probed, key)
	return f.tls[key]
}

const sampleXML = `<?xml version="1.0"?>
<clientConfig version="1.1"><emailProvider id="example.org">
  <incomingServer type="pop3"><hostname>pop.example.org</hostname><port>995</port><socketType>SSL</socketType></incomingServer>
  <incomingServer type="imap"><hostname>imap.example.org</hostname><port>143</port><socketType>plain</socketType></incomingServer>
  <incomingServer type="imap"><hostname>IMAP.example.org</hostname><port>993</port><socketType>SSL</socketType><username>%EMAILLOCALPART%</username></incomingServer>
  <outgoingServer type="smtp"><hostname>smtp.example.org</hostname><port>25</port><socketType>STARTTLS</socketType></outgoingServer>
  <outgoingServer type="smtp"><hostname>smtp.example.org</hostname><port>587</port><socketType>STARTTLS</socketType></outgoingServer>
</emailProvider></clientConfig>`

func TestDetectOrderAndSources(t *testing.T) {
	ctx := context.Background()

	// 1. Built-in: no network at all.
	f := &fakeNet{}
	s, err := Detect(ctx, "Someone@Gmail.com", f, Options{AllowISPDB: true})
	if err != nil || s.IMAPHost != "imap.gmail.com" || s.SMTPPort != 587 || s.AuthMethod != "app-password" || s.Provider != "Google" {
		t.Fatalf("gmail: %+v %v", s, err)
	}
	if len(f.fetched)+len(f.probed) != 0 {
		t.Fatalf("built-in lookup touched the network: %v %v", f.fetched, f.probed)
	}

	// 2. MX: a custom domain hosted at Google / Microsoft / Seznam.
	f = &fakeNet{mx: map[string][]string{
		"ecomail.test": {"aspmx.l.google.com"},
		"corp.test":    {"corp-test.mail.protection.outlook.com"},
		"gause.test":   {"mx1.seznam.cz"},
	}}
	if s, _ := Detect(ctx, "jakub@ecomail.test", f, Options{}); s.Provider != "Google" || !strings.Contains(s.Source, "aspmx.l.google.com") {
		t.Fatalf("google workspace: %+v", s)
	}
	if s, _ := Detect(ctx, "a@corp.test", f, Options{}); s.AuthMethod != "oauth2-outlook" {
		t.Fatalf("microsoft 365: %+v", s)
	}
	if s, _ := Detect(ctx, "a@gause.test", f, Options{}); s.IMAPHost != "imap.seznam.cz" || s.AuthMethod != "plain" {
		t.Fatalf("seznam: %+v", s)
	}
	// A look-alike MX must not match a provider.
	f = &fakeNet{mx: map[string][]string{"evil.test": {"aspmx.l.google.com.evil.test", "notgoogle.com"}}}
	if s, err := Detect(ctx, "a@evil.test", f, Options{}); err == nil {
		t.Fatalf("look-alike MX matched: %+v", s)
	}

	// 3. The domain's own autoconfig; TLS-only servers, plain ones skipped.
	f = &fakeNet{pages: map[string]string{"https://autoconfig.example.org/mail/config-v1.1.xml": sampleXML}}
	s, err = Detect(ctx, "bob@example.org", f, Options{})
	if err != nil || s.IMAPHost != "imap.example.org" || s.IMAPPort != 993 || s.SMTPHost != "smtp.example.org" || s.SMTPPort != 587 ||
		ExpandUsername(s.Username, "bob@example.org") != "bob" {
		t.Fatalf("autoconfig: %+v %v", s, err)
	}

	// 4. ISPDB only with permission.
	f = &fakeNet{pages: map[string]string{"https://autoconfig.thunderbird.net/v1.1/example.org": sampleXML}}
	if _, err := Detect(ctx, "bob@example.org", f, Options{}); err == nil {
		t.Fatal("ISPDB used without permission")
	}
	for _, url := range f.fetched {
		if strings.Contains(url, "thunderbird.net") {
			t.Fatalf("ISPDB contacted without permission: %s", url)
		}
	}
	if s, err := Detect(ctx, "bob@example.org", f, Options{AllowISPDB: true}); err != nil || !strings.Contains(s.Source, "Mozilla") {
		t.Fatalf("ISPDB: %+v %v", s, err)
	}

	// 5. SRV.
	f = &fakeNet{srv: map[string]string{"imaps srv.test": "mx.srv.test:993", "submission srv.test": "mx.srv.test:587"}}
	if s, err := Detect(ctx, "a@srv.test", f, Options{}); err != nil || s.IMAPHost != "mx.srv.test" || s.SMTPPort != 587 {
		t.Fatalf("srv: %+v %v", s, err)
	}

	// 6. Guess: only hosts that answer with valid TLS.
	f = &fakeNet{tls: map[string]bool{"mail.small.test:993": true, "mail.small.test:465": true}}
	if s, err := Detect(ctx, "a@small.test", f, Options{}); err != nil || s.IMAPHost != "mail.small.test" || s.SMTPPort != 465 {
		t.Fatalf("guess: %+v %v", s, err)
	}
	if _, err := Detect(ctx, "a@nothing.test", &fakeNet{}, Options{}); err != ErrNotFound {
		t.Fatalf("nothing found: %v", err)
	}
}

func TestUntrustedAnswersAreRefused(t *testing.T) {
	ctx := context.Background()
	for _, addr := range []string{"", "nope", "a@", "@b.test", "a@b", "a b@c.test", "a@c.test\r\nX: y", "a@-bad.test", "a@127.0.0.1"} {
		if _, err := Detect(ctx, addr, &fakeNet{}, Options{}); err == nil || err == ErrNotFound {
			t.Errorf("address %q: err = %v, want a validation error", addr, err)
		}
	}
	// Autoconfig naming an IP, a host with a port, or only plaintext servers.
	for name, doc := range map[string]string{
		"ip host":   strings.ReplaceAll(sampleXML, "IMAP.example.org", "10.0.0.1"),
		"host:port": strings.ReplaceAll(sampleXML, "IMAP.example.org", "imap.example.org:993/x"),
		"plaintext": strings.ReplaceAll(strings.ReplaceAll(sampleXML, "<socketType>SSL</socketType>", "<socketType>plain</socketType>"), "STARTTLS", "plain"),
		"not xml":   "<html>login</html>",
	} {
		if s, ok := parseAutoconfig([]byte(doc)); ok {
			t.Errorf("%s: accepted %+v", name, s)
		}
	}
	// SRV pointing at something that is not a host name.
	f := &fakeNet{srv: map[string]string{"imaps bad.test": "10.0.0.1:993", "submission bad.test": "smtp.bad.test:587"}}
	if s, err := Detect(ctx, "a@bad.test", f, Options{}); err == nil {
		t.Errorf("SRV with an IP target accepted: %+v", s)
	}
	for in, want := range map[string]string{"": "a@b.test", "%EMAILLOCALPART%": "a", "%EMAILADDRESS%": "a@b.test", "%WEIRD%": "a@b.test", "x y": "a@b.test", "fixed": "fixed"} {
		if got := ExpandUsername(in, "a@b.test"); got != want {
			t.Errorf("ExpandUsername(%q) = %q, want %q", in, got, want)
		}
	}
}
