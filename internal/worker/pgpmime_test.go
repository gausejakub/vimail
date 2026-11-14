package worker

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp/pgptest"
)

func TestPGPSignedMessageRoundTrip(t *testing.T) {
	c := pgptest.Keyring(t, "me@x.test", "you@y.test")
	req := SendRequest{
		From: "Me <me@x.test>", ToList: []string{"you@y.test"}, Subject: "signed žluťoučký",
		Body: "Hello,\n\nline with trailing space \nPříliš žluťoučký kůň.\n", Sign: true,
		Attachments: []email.DraftAttachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("attached")}},
	}
	_, raw, err := ComposeMessage(req, ComposeOptions{PGP: c})
	if err != nil {
		t.Fatal(err)
	}
	if ct := headerValueOf(raw, "Content-Type"); !strings.HasPrefix(ct, "multipart/signed") || !strings.Contains(ct, "application/pgp-signature") || !strings.Contains(ct, "pgp-sha256") {
		t.Fatalf("content type = %q", ct)
	}
	if headerValueOf(raw, "Subject") == "" || headerValueOf(raw, "From") == "" {
		t.Fatal("message headers missing")
	}
	res, err := ParseBodyPGP(raw, c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Security, "signed by Test <me@x.test>") || strings.Contains(res.Security, "not verified") || strings.Contains(res.Security, "differs") {
		t.Fatalf("security = %q", res.Security)
	}
	if !strings.Contains(res.Text, "Příliš žluťoučký kůň.") || len(res.Attachments) != 1 || res.Attachments[0].Filename != "a.txt" {
		t.Fatalf("content: %q attachments=%+v", res.Text, res.Attachments)
	}

	// Any change to the signed content is a bad signature; the text is
	// still shown, with the warning.
	tampered := bytes.Replace(raw, []byte("Hello"), []byte("Jello"), 1)
	res, _ = ParseBodyPGP(tampered, c)
	if !strings.HasPrefix(res.Security, "BAD SIGNATURE") {
		t.Fatalf("tampered message: security = %q", res.Security)
	}
	// Without PGP enabled nothing is claimed either way.
	if res, _ := ParseBodyPGP(raw, nil); res.Security != "" {
		t.Fatalf("security without a client: %q", res.Security)
	}
	// A message that only writes the words into its body gets no verdict.
	_, fake, _ := ComposeMessage(SendRequest{From: "evil@z.test", ToList: []string{"me@x.test"}, Body: "signed by Test <me@x.test>\n\nPay now."}, ComposeOptions{})
	if res, _ := ParseBodyPGP(fake, c); res.Security != "" {
		t.Fatalf("fake verdict accepted: %q", res.Security)
	}
	// A valid signature by someone else than the sender is pointed out.
	spoof := bytes.Replace(raw, []byte("me@x.test>\r\nTo:"), []byte("ceo@corp.test>\r\nTo:"), 1)
	if !bytes.Contains(spoof, []byte("ceo@corp.test")) {
		spoof = bytes.Replace(raw, []byte("<me@x.test>"), []byte("<ceo@corp.test>"), 1)
	}
	if res, _ := ParseBodyPGP(spoof, c); !strings.Contains(res.Security, "differs from the sender ceo@corp.test") {
		t.Fatalf("sender mismatch not pointed out: %q", res.Security)
	}
}

func TestPGPEncryptedMessageRoundTrip(t *testing.T) {
	c := pgptest.Keyring(t, "me@x.test", "you@y.test", "boss@w.test")
	req := SendRequest{
		From: "me@x.test", ToList: []string{"You <you@y.test>"}, Bcc: []string{"boss@w.test"}, Subject: "the subject is not encrypted",
		Body: "the secret plan\n", Sign: true, Encrypt: true,
	}
	memo := &pgpMemo{}
	_, wire, err := ComposeMessage(req, ComposeOptions{PGP: c, memo: memo})
	if err != nil {
		t.Fatal(err)
	}
	_, keep, err := ComposeMessage(req, ComposeOptions{PGP: c, memo: memo, IncludeBcc: true})
	if err != nil {
		t.Fatal(err)
	}
	if ct := headerValueOf(wire, "Content-Type"); !strings.HasPrefix(ct, "multipart/encrypted") || !strings.Contains(ct, "application/pgp-encrypted") {
		t.Fatalf("content type = %q", ct)
	}
	for _, leak := range []string{"secret plan", "boss@w.test"} {
		if bytes.Contains(wire, []byte(leak)) {
			t.Fatalf("%q readable in the transmitted message", leak)
		}
	}
	// The kept copy has the Bcc header and the very same encrypted content.
	_, wireBody := splitHeader(wire)
	_, keepBody := splitHeader(keep)
	if !bytes.Equal(wireBody, keepBody) || !strings.Contains(headerValueOf(keep, "Bcc"), "boss@w.test") || headerValueOf(wire, "Bcc") != "" {
		t.Fatal("kept copy and transmitted message differ in more than the Bcc header")
	}

	res, err := ParseBodyPGP(wire, c)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Decrypted || !strings.Contains(res.Text, "the secret plan") || !strings.HasPrefix(res.Security, "encrypted; signed by Test <me@x.test>") {
		t.Fatalf("decrypted: %+v", res)
	}

	// The draft copy is encrypted to the author alone.
	_, draft, err := ComposeMessage(req, ComposeOptions{PGP: c, DraftCopy: true, IncludeBcc: true})
	if err != nil {
		t.Fatal(err)
	}
	other := pgptest.Keyring(t, "you@y.test") // a different person's keyring
	if res, _ := ParseBodyPGP(draft, other); res.Decrypted || strings.Contains(res.Text, "secret plan") || !strings.HasPrefix(res.Security, "encrypted; not decrypted") {
		t.Fatalf("someone else read the draft: %+v", res)
	}
	if res, _ := ParseBodyPGP(draft, c); !res.Decrypted {
		t.Fatalf("author cannot read the draft: %+v", res)
	}

	// A recipient without a key: nothing is produced, and the error says who.
	req.ToList = []string{"nokey@z.test"}
	if _, raw, err := ComposeMessage(req, ComposeOptions{PGP: c}); err == nil || raw != nil || !strings.Contains(err.Error(), "nokey@z.test") {
		t.Fatalf("missing key: %v", err)
	}
	// Asking for PGP without it being configured is an error, not plain text.
	if _, raw, err := ComposeMessage(req, ComposeOptions{}); err == nil || raw != nil {
		t.Fatal("message built in the clear although encryption was asked for")
	}
}
