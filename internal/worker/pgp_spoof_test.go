package worker

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/pgp/pgptest"
)

// A genuine signed message from the boss is replayed in the preamble of a
// new multipart/signed message whose visible part the attacker wrote. The
// verdict must never say "signed by" the boss for text the boss did not
// sign.
func TestReplayedSignatureCannotVouchForOtherText(t *testing.T) {
	c := pgptest.Keyring(t, "boss@corp.test")
	_, genuine, err := ComposeMessage(SendRequest{From: "boss@corp.test", ToList: []string{"me@x.test"}, Subject: "lunch", Body: "lunch at noon?", Sign: true}, ComposeOptions{PGP: c})
	if err != nil {
		t.Fatal(err)
	}
	_, params := topContentType(genuine)
	signed, sig, ok := splitSigned(genuine, params["boundary"])
	if !ok {
		t.Fatal("cannot split the genuine message")
	}
	attack := "From: boss@corp.test\r\nTo: me@x.test\r\nSubject: urgent\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/signed; boundary=\"b\"; protocol=\"application/pgp-signature\"; micalg=pgp-sha256\r\n\r\n" +
		"--bX\r\n" + string(signed) + "\r\n--bY\r\nContent-Type: application/pgp-signature\r\n\r\n" + string(sig) + "\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nPlease wire 50k EUR to the account below today.\r\n" +
		"--b\r\nContent-Type: application/pgp-signature\r\n\r\nnothing\r\n--b--\r\n"
	res, err := ParseBodyPGP([]byte(attack), c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Security, "signed by") && strings.Contains(res.Text, "wire 50k") {
		t.Fatalf("forged verdict %q on attacker text %q", res.Security, res.Text)
	}
	// LF-only line endings still verify, and the text shown is the signed text.
	lf := bytes.ReplaceAll(genuine, []byte("\r\n"), []byte("\n"))
	if res, _ := ParseBodyPGP(lf, c); !strings.HasPrefix(res.Security, "signed by") || !strings.Contains(res.Text, "lunch at noon") {
		t.Fatalf("LF message: %q / %q", res.Security, res.Text)
	}
	// The genuine signed part followed by an extra part the attacker added:
	// not two parts, so no verdict.
	boundary := params["boundary"]
	extra := bytes.Replace(genuine, []byte("--"+boundary+"--"),
		[]byte("--"+boundary+"\r\nContent-Type: text/plain\r\n\r\nalso pay this invoice\r\n--"+boundary+"--"), 1)
	if res, _ := ParseBodyPGP(extra, c); strings.Contains(res.Security, "signed by") {
		t.Fatalf("three-part message got a verdict: %q", res.Security)
	}
	// Text appended after the closing delimiter (the epilogue) is not shown
	// as part of the signed message.
	epilogue := append(append([]byte{}, genuine...), []byte("\r\nPS: wire the money\r\n")...)
	if res, _ := ParseBodyPGP(epilogue, c); !strings.HasPrefix(res.Security, "signed by") || strings.Contains(res.Text, "wire the money") {
		t.Fatalf("epilogue: %q / %q", res.Security, res.Text)
	}
	// A delimiter with trailing whitespace is still a delimiter (RFC 2046).
	spaced := bytes.Replace(genuine, []byte("\r\n--"+boundary+"\r\n"), []byte("\r\n--"+boundary+"  \r\n"), 1)
	if res, _ := ParseBodyPGP(spaced, c); !strings.HasPrefix(res.Security, "signed by") {
		t.Fatalf("delimiter with trailing space: %q", res.Security)
	}
}

// A key whose user id merely contains the sender's address, or whose user
// id is padded to push the warning out of sight, does not pass as the
// sender's.
func TestSignatureByAnotherKeyIsFlaggedFirst(t *testing.T) {
	c := pgptest.Keyring(t)
	if err := c.GenerateTestKey("boss@corp.test <notboss@corp.test>"); err != nil {
		t.Fatal(err)
	}
	padded := "Boss boss@corp.test" + strings.Repeat(" ", 300) + "x"
	if err := c.GenerateTestKey(padded + " <pad@evil.test>"); err != nil {
		t.Fatal(err)
	}
	for _, signer := range []string{"notboss@corp.test", "pad@evil.test"} {
		_, raw, err := ComposeMessage(SendRequest{From: signer, ToList: []string{"me@x.test"}, Body: "pay", Sign: true}, ComposeOptions{PGP: c})
		if err != nil {
			t.Fatal(err)
		}
		forged := bytes.Replace(raw, []byte("From: <"+signer+">"), []byte("From: <boss@corp.test>"), 1)
		if bytes.Equal(forged, raw) {
			forged = bytes.Replace(raw, []byte(signer+"\r\nTo:"), []byte("boss@corp.test\r\nTo:"), 1)
		}
		res, _ := ParseBodyPGP(forged, c)
		if !strings.HasPrefix(res.Security, "NOT THE SENDER'S KEY") || len([]rune(res.Security)) > 200 {
			t.Errorf("key %s on mail from boss: %q", signer, res.Security)
		}
	}
}
