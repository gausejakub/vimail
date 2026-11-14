// Package pgptest gives tests a throwaway GnuPG keyring.
package pgptest

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/pgp"
)

// Keyring creates a temporary GnuPG home with a key (no passphrase) for
// each address and returns a client using it. It never touches the user's
// own keyring. The test is skipped when gpg is not installed.
func Keyring(t testing.TB, addresses ...string) *pgp.Client {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg not installed")
	}
	// A short path: gpg-agent's socket path has a length limit.
	home, err := os.MkdirTemp("", "vgpg")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(home, 0700)
	c := &pgp.Client{Home: home, Timeout: 60 * time.Second}
	t.Cleanup(func() {
		kill := exec.Command("gpgconf", "--kill", "all")
		kill.Env = append(os.Environ(), "GNUPGHOME="+home)
		kill.Run()
		os.RemoveAll(home)
	})
	for _, addr := range addresses {
		if err := c.GenerateTestKey("Test <" + addr + ">"); err != nil {
			t.Fatalf("generate key for %s: %v", addr, err)
		}
	}
	return c
}
