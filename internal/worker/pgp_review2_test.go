package worker

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp/pgptest"
)

// encryptedEnvelope wraps an OpenPGP message as multipart/encrypted, with
// optional extra outer parts after it.
func encryptedEnvelope(from string, cipher []byte, extra ...string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\nTo: me@x.test\r\nSubject: s\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"X\"\r\n\r\n")
	b.WriteString("--X\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n")
	b.WriteString("--X\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\n\r\n")
	b.Write(bytes.TrimRight(crlf(cipher), "\r\n"))
	b.WriteString("\r\n")
	for _, part := range extra {
		b.WriteString("--X\r\n" + part + "\r\n")
	}
	b.WriteString("--X--\r\n")
	return b.Bytes()
}

// A genuine signed message with an attachment, encrypted to the victim and
// sent with a cleartext attachment (or invitation) of the same name next to
// it: the extra part must never be saved, forwarded or answered in place of
// the protected content, and the message gets no "signed by" verdict.
func TestCleartextPartNextToEncryptedContentIsNeverUsed(t *testing.T) {
	c := pgptest.Keyring(t, "boss@corp.test", "me@x.test")
	_, genuine, err := ComposeMessage(SendRequest{
		From: "boss@corp.test", ToList: []string{"list@corp.test"}, Subject: "report", Body: "the report", Sign: true,
		Attachments: []email.DraftAttachment{{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("GENUINE")}},
	}, ComposeOptions{PGP: c})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := c.Encrypt(genuine, []string{"me@x.test"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	evilPDF := "Content-Type: application/pdf; name=\"report.pdf\"\r\nContent-Disposition: attachment; filename=\"report.pdf\"\r\n\r\nMALLORY"
	evilICS := "Content-Type: text/calendar; method=REQUEST\r\n\r\nBEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:x\r\n" +
		"DTSTART:20261112T090000Z\r\nORGANIZER:mailto:mallory@evil.test\r\nEND:VEVENT\r\nEND:VCALENDAR"

	// The well-formed message: the attachment is the genuine one.
	good := encryptedEnvelope("boss@corp.test", cipher)
	content, err := effectiveContent(good, c)
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := ExtractAttachmentData(content)
	if len(parts) != 1 || string(parts[0].Data) != "GENUINE" {
		t.Fatalf("well-formed message: attachments %+v", parts)
	}
	if res, _ := ParseBodyPGP(good, c); !strings.HasPrefix(res.Security, "encrypted; signed by Test <boss@corp.test>") {
		t.Fatalf("well-formed message: %q", res.Security)
	}

	// With an extra cleartext part: no verdict, nothing to act on.
	for name, extra := range map[string]string{"attachment": evilPDF, "invitation": evilICS} {
		evil := encryptedEnvelope("boss@corp.test", cipher, extra)
		if res, _ := ParseBodyPGP(evil, c); strings.Contains(res.Security, "signed by") || strings.Contains(res.Text, "the report") {
			t.Errorf("%s: verdict %q, text %q", name, res.Security, res.Text)
		}
		if content, err := effectiveContent(evil, c); err == nil {
			parts, _ := ExtractAttachmentData(content)
			for _, p := range parts {
				if string(p.Data) == "MALLORY" {
					t.Errorf("%s: the cleartext attachment would be saved", name)
				}
			}
			if ExtractCalendar(content) != nil {
				t.Errorf("%s: the cleartext invitation would be answered", name)
			}
		}
	}
}

// The sender a key is matched against is the outer message's From, and a
// From that cannot be read is a warning, never a pass.
func TestSenderCannotBeHiddenOrReplaced(t *testing.T) {
	c := pgptest.Keyring(t, "mallory@corp.test", "me@x.test")
	_, signed, err := ComposeMessage(SendRequest{From: "mallory@corp.test", ToList: []string{"me@x.test"}, Subject: "s", Body: "pay now", Sign: true}, ComposeOptions{PGP: c})
	if err != nil {
		t.Fatal(err)
	}

	// Unparsable From on a signed message (the mail server would still
	// show "Alice <alice@corp.test").
	head, body := splitHeader(signed)
	var lines []string
	for _, l := range strings.Split(string(head), "\r\n") {
		if strings.HasPrefix(strings.ToLower(l), "from:") {
			l = "From: Alice <alice@corp.test"
		}
		lines = append(lines, l)
	}
	broken := []byte(strings.Join(lines, "\r\n") + "\r\n\r\n" + string(body))
	if res, _ := ParseBodyPGP(broken, c); !strings.HasPrefix(res.Security, "SENDER UNKNOWN") {
		t.Fatalf("unparsable From: %q", res.Security)
	}
	// No From at all.
	noFrom := bytes.Replace(broken, []byte("From: Alice <alice@corp.test\r\n"), nil, 1)
	if res, _ := ParseBodyPGP(noFrom, c); !strings.HasPrefix(res.Security, "SENDER UNKNOWN") {
		t.Fatalf("missing From: %q", res.Security)
	}

	// Encrypted message from "alice" whose content carries Mallory's own
	// From and signature: matched against alice, so flagged.
	cipher, err := c.Encrypt(signed, []string{"me@x.test"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := ParseBodyPGP(encryptedEnvelope("Alice <alice@corp.test>", cipher), c)
	if !strings.Contains(res.Security, "NOT THE SENDER'S KEY") || !strings.Contains(res.Security, "not for the sender alice@corp.test") {
		t.Fatalf("inner From replaced the sender: %q", res.Security)
	}
}
