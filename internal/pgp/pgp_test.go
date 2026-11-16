package pgp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/pgp"
	"github.com/gausejakub/vimail/internal/pgp/pgptest"
)

func TestSignVerifyEncryptDecrypt(t *testing.T) {
	c := pgptest.Keyring(t, "me@x.test", "you@y.test")
	data := []byte("Content-Type: text/plain\r\n\r\nhello\r\n")

	sig, err := c.Sign(data, "me@x.test")
	if err != nil || !strings.Contains(string(sig), "BEGIN PGP SIGNATURE") {
		t.Fatalf("sign: %v %q", err, sig)
	}
	got, err := c.Verify(data, sig, "me@x.test")
	if err != nil || !got.Good() || !got.Trusted || !strings.Contains(got.Signer, "me@x.test") {
		t.Fatalf("verify: %+v %v", got, err)
	}
	// One changed byte: a bad signature, reported as such.
	if got, _ := c.Verify(append([]byte("X"), data...), sig, "me@x.test"); got.Status != "bad" || got.Good() {
		t.Fatalf("tampered data: %+v", got)
	}
	if got, _ := c.Verify(data, []byte("not a signature"), "me@x.test"); got.Good() {
		t.Fatalf("garbage signature accepted: %+v", got)
	}

	// Encrypt + sign to two recipients, one of them hidden.
	enc, err := c.Encrypt(data, []string{"you@y.test"}, []string{"me@x.test"}, "me@x.test")
	if err != nil || !strings.Contains(string(enc), "BEGIN PGP MESSAGE") || strings.Contains(string(enc), "hello") {
		t.Fatalf("encrypt: %v", err)
	}
	plain, insideSig, err := c.Decrypt(enc, "me@x.test")
	if err != nil || string(plain) != string(data) || insideSig == nil || !insideSig.Good() {
		t.Fatalf("decrypt: %q sig=%+v err=%v", plain, insideSig, err)
	}
	// Unsigned encryption reports no signature.
	enc, _ = c.Encrypt(data, []string{"me@x.test"}, nil, "")
	if _, insideSig, err := c.Decrypt(enc, "me@x.test"); err != nil || insideSig != nil {
		t.Fatalf("unsigned: sig=%+v err=%v", insideSig, err)
	}
	if _, _, err := c.Decrypt([]byte("-----BEGIN PGP MESSAGE-----\n\nbm90IHJlYWw=\n-----END PGP MESSAGE-----\n"), ""); err == nil {
		t.Fatal("garbage decrypted")
	}
}

func TestMissingKeysAndAddressMatching(t *testing.T) {
	c := pgptest.Keyring(t, "me@x.test", "bob@y.test")
	// No key: a clear error naming the address, and CanEncryptTo agrees.
	_, err := c.Encrypt([]byte("x"), []string{"me@x.test", "nokey@z.test"}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "nokey@z.test") {
		t.Fatalf("missing key: %v", err)
	}
	if err := c.CanEncryptTo([]string{"me@x.test", "bob@y.test"}); err != nil {
		t.Fatalf("CanEncryptTo: %v", err)
	}
	if err := c.CanEncryptTo([]string{"nokey@z.test"}); err == nil {
		t.Fatal("CanEncryptTo accepted an address without a key")
	}
	// An address is matched exactly: "ob@y.test" is not Bob's key, and an
	// option-looking or malformed "address" never reaches gpg as a flag.
	for _, bad := range []string{"ob@y.test", "y.test", "--export-secret-keys@x", "a b@x.test", "me@x.test\n--yes", ""} {
		if _, err := c.Encrypt([]byte("x"), []string{bad}, nil, ""); err == nil {
			t.Errorf("recipient %q accepted", bad)
		}
		if _, err := c.Sign([]byte("x"), bad); err == nil {
			t.Errorf("signer %q accepted", bad)
		}
	}
	// A signature by a key that is not in the keyring is "unknown key".
	other := pgptest.Keyring(t, "stranger@w.test")
	sig, err := other.Sign([]byte("data"), "stranger@w.test")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Verify([]byte("data"), sig, "stranger@w.test"); got.Status != "unknown key" || got.Good() || got.Trusted {
		t.Fatalf("unknown signer: %+v", got)
	}
	// No gpg at all.
	if _, err := (&pgp.Client{Bin: "/nonexistent/gpg"}).Sign([]byte("x"), "me@x.test"); err == nil {
		t.Fatal("missing gpg binary not reported")
	}
}

// A few kilobytes of ciphertext can decompress to gigabytes. The output is
// bounded and nothing of such a message is returned.
func TestDecryptRefusesDecompressionBomb(t *testing.T) {
	c := pgptest.Keyring(t, "alice@example.com")
	old := pgp.MaxOutput
	pgp.MaxOutput = 1 << 20
	defer func() { pgp.MaxOutput = old }()

	cipher, err := c.Encrypt(make([]byte, 8<<20), []string{"alice@example.com"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cipher) > 1<<20 {
		t.Fatalf("test needs a compressed ciphertext, got %d bytes", len(cipher))
	}
	plain, sig, err := c.Decrypt(cipher, "alice@example.com")
	if !errors.Is(err, pgp.ErrTooLarge) || plain != nil || sig != nil {
		t.Fatalf("Decrypt = %d bytes, %v, %v; want ErrTooLarge and nothing else", len(plain), sig, err)
	}
	// An ordinary message still decrypts.
	small, err := c.Encrypt([]byte("hello"), []string{"alice@example.com"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if plain, _, err := c.Decrypt(small, "alice@example.com"); err != nil || string(plain) != "hello" {
		t.Fatalf("Decrypt = %q, %v", plain, err)
	}
}
