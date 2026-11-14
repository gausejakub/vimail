// Package pgp signs, encrypts, verifies and decrypts with the user's own
// GnuPG: vimail holds no keys and asks no passphrases; gpg and its agent do.
//
// gpg is always run directly (no shell), in batch mode, and its verdicts are
// read from the machine-readable status output, never from its messages.
package pgp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Client runs gpg.
type Client struct {
	// Bin is the gpg binary; empty means "gpg".
	Bin string
	// Home, when set, is used as GNUPGHOME (tests use a throwaway keyring).
	Home string
	// Timeout bounds one gpg run, which may wait for the user to enter a
	// passphrase. Zero means two minutes.
	Timeout time.Duration
}

// Signature is the outcome of checking a signature.
type Signature struct {
	// Status is "good", "bad", "unknown key", "expired" or "error".
	Status string
	// Signer is the key's user id as gpg reports it (text from the key, not
	// from the message). KeyID is the long key id or fingerprint.
	Signer string
	KeyID  string
	// Trusted reports that gpg considers the key valid for that user id
	// (full or ultimate trust). A good signature from an untrusted key
	// proves only that some key signed the message.
	Trusted bool
}

// Good reports a valid signature.
func (s Signature) Good() bool { return s.Status == "good" }

// ErrNoGPG means the gpg binary could not be run.
var ErrNoGPG = errors.New("gpg is not available")

func (c *Client) bin() string {
	if c != nil && c.Bin != "" {
		return c.Bin
	}
	return "gpg"
}

// run executes gpg with stdin and returns stdout and the status lines.
func (c *Client) run(stdin []byte, args ...string) (stdout []byte, status []string, err error) {
	timeout := 2 * time.Minute
	if c != nil && c.Timeout > 0 {
		timeout = c.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Status goes to its own pipe (fd 3) so it cannot be confused with
	// output or with gpg's human-readable messages.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	full := append([]string{"--batch", "--no-tty", "--status-fd", "3", "--display-charset", "utf-8"}, args...)
	cmd := exec.CommandContext(ctx, c.bin(), full...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.ExtraFiles = []*os.File{pw}
	cmd.Env = os.Environ()
	if c != nil && c.Home != "" {
		cmd.Env = append(cmd.Env, "GNUPGHOME="+c.Home)
	}
	if startErr := cmd.Start(); startErr != nil {
		pr.Close()
		pw.Close()
		return nil, nil, fmt.Errorf("%w: %v", ErrNoGPG, startErr)
	}
	pw.Close()
	var statusBuf bytes.Buffer
	done := make(chan struct{})
	go func() { statusBuf.ReadFrom(pr); close(done) }()
	runErr := cmd.Wait()
	<-done
	pr.Close()
	for _, line := range strings.Split(statusBuf.String(), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "[GNUPG:] "); ok {
			status = append(status, rest)
		}
	}
	if runErr != nil {
		msg := lastLine(errOut.String())
		if ctx.Err() != nil {
			msg = "timed out"
		}
		return out.Bytes(), status, fmt.Errorf("gpg: %s", msg)
	}
	return out.Bytes(), status, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	line = strings.TrimPrefix(line, "gpg: ")
	if line == "" {
		return "failed"
	}
	return line
}

// exact makes gpg match a key by exactly this address, never by a
// substring of a name or address on some other key.
func exact(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.ContainsAny(addr, "<> \t\r\n") || !strings.Contains(addr, "@") || strings.HasPrefix(addr, "-") {
		return "", fmt.Errorf("not an address: %q", addr)
	}
	return "<" + addr + ">", nil
}

// Sign returns an armored detached signature over data, made with the key
// of the signer address.
func (c *Client) Sign(data []byte, signer string) ([]byte, error) {
	user, err := exact(signer)
	if err != nil {
		return nil, err
	}
	sig, _, err := c.run(data, "--armor", "--detach-sign", "--digest-algo", "SHA256", "--local-user", user)
	if err != nil {
		return nil, fmt.Errorf("signing as %s: %w", signer, err)
	}
	return sig, nil
}

// Encrypt returns data encrypted (armored) to the recipients' keys.
// hidden recipients can decrypt but are not named in the result (Bcc).
// With a signer, the data is signed inside the encryption.
func (c *Client) Encrypt(data []byte, recipients, hidden []string, signer string) ([]byte, error) {
	args := []string{"--armor", "--encrypt"}
	if signer != "" {
		user, err := exact(signer)
		if err != nil {
			return nil, err
		}
		args = append(args, "--sign", "--digest-algo", "SHA256", "--local-user", user)
	}
	if len(recipients)+len(hidden) == 0 {
		return nil, fmt.Errorf("no recipients to encrypt to")
	}
	for _, r := range recipients {
		user, err := exact(r)
		if err != nil {
			return nil, err
		}
		args = append(args, "--recipient", user)
	}
	for _, r := range hidden {
		user, err := exact(r)
		if err != nil {
			return nil, err
		}
		args = append(args, "--hidden-recipient", user)
	}
	out, status, err := c.run(data, args...)
	if err != nil {
		// Name the recipient whose key is missing or not trusted.
		for _, line := range status {
			if rest, ok := strings.CutPrefix(line, "INV_RECP "); ok {
				if f := strings.Fields(rest); len(f) == 2 {
					return nil, fmt.Errorf("no usable public key for %s (import it and mark it trusted in gpg)", strings.Trim(f[1], "<>"))
				}
			}
		}
		return nil, fmt.Errorf("encrypting: %w", err)
	}
	return out, nil
}

// CanEncryptTo reports the first address there is no usable public key for.
func (c *Client) CanEncryptTo(addresses []string) error {
	for _, a := range addresses {
		if _, err := c.Encrypt([]byte("x"), []string{a}, nil, ""); err != nil {
			return err
		}
	}
	return nil
}

// Verify checks a detached signature over data.
func (c *Client) Verify(data, signature []byte) (Signature, error) {
	dir, err := os.MkdirTemp("", "vimail-pgp-")
	if err != nil {
		return Signature{Status: "error"}, err
	}
	defer os.RemoveAll(dir)
	sigPath, dataPath := dir+"/sig.asc", dir+"/data"
	if err := os.WriteFile(sigPath, signature, 0600); err != nil {
		return Signature{Status: "error"}, err
	}
	if err := os.WriteFile(dataPath, data, 0600); err != nil {
		return Signature{Status: "error"}, err
	}
	_, status, runErr := c.run(nil, "--verify", sigPath, dataPath)
	sig := parseSignature(status)
	if errors.Is(runErr, ErrNoGPG) {
		return sig, runErr
	}
	return sig, nil
}

// Decrypt decrypts data. When the content was signed inside the
// encryption, the signature's outcome is returned too.
func (c *Client) Decrypt(data []byte) ([]byte, *Signature, error) {
	out, status, err := c.run(data, "--decrypt")
	decrypted := false
	for _, line := range status {
		if line == "DECRYPTION_OKAY" {
			decrypted = true
		}
	}
	if !decrypted {
		if err == nil {
			err = errors.New("gpg: not decrypted")
		}
		for _, line := range status {
			if strings.HasPrefix(line, "NO_SECKEY") {
				err = errors.New("no secret key to decrypt this message")
			}
		}
		return nil, nil, err
	}
	var sig *Signature
	for _, line := range status {
		if strings.HasPrefix(line, "GOODSIG ") || strings.HasPrefix(line, "BADSIG ") || strings.HasPrefix(line, "ERRSIG ") ||
			strings.HasPrefix(line, "EXPSIG ") || strings.HasPrefix(line, "EXPKEYSIG ") || strings.HasPrefix(line, "REVKEYSIG ") {
			s := parseSignature(status)
			sig = &s
			break
		}
	}
	return out, sig, nil
}

// parseSignature reads gpg's verdict from its status lines.
func parseSignature(status []string) Signature {
	sig := Signature{Status: "error"}
	for _, line := range status {
		keyword, rest, _ := strings.Cut(line, " ")
		id, name, _ := strings.Cut(rest, " ")
		switch keyword {
		case "GOODSIG":
			sig.Status, sig.KeyID, sig.Signer = "good", id, name
		case "BADSIG":
			sig.Status, sig.KeyID, sig.Signer = "bad", id, name
		case "EXPSIG", "EXPKEYSIG", "REVKEYSIG":
			sig.Status, sig.KeyID, sig.Signer = "expired", id, name
		case "ERRSIG":
			// The last field 9 means the public key is missing.
			sig.Status, sig.KeyID = "error", id
			if f := strings.Fields(rest); len(f) >= 6 && f[5] == "9" {
				sig.Status = "unknown key"
			}
		case "NO_PUBKEY":
			sig.Status, sig.KeyID = "unknown key", id
		case "VALIDSIG":
			sig.KeyID = id
		case "TRUST_FULLY", "TRUST_ULTIMATE":
			sig.Trusted = true
		}
	}
	return sig
}

// GenerateTestKey creates a key without a passphrase. It refuses to run on
// the user's own keyring: Home must be set.
func (c *Client) GenerateTestKey(userID string) error {
	if c == nil || c.Home == "" {
		return errors.New("test keys are only generated in a separate GnuPG home")
	}
	_, _, err := c.run(nil, "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key", userID, "default", "default", "never")
	return err
}

// HasSecretKey reports whether there is a secret key for exactly this
// address, i.e. whether mail can be signed as it.
func (c *Client) HasSecretKey(addr string) error {
	user, err := exact(addr)
	if err != nil {
		return err
	}
	if _, _, err := c.run(nil, "--list-secret-keys", "--with-colons", user); err != nil {
		if errors.Is(err, ErrNoGPG) {
			return err
		}
		return fmt.Errorf("no secret key for %s in your GnuPG keyring", addr)
	}
	return nil
}
