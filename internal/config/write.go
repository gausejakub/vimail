package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Path returns the location of the config file.
func Path() string { return configPath() }

// AccountBlock renders an account as the TOML block AppendAccount writes.
// Only fields that are set are written.
func AccountBlock(a AccountConfig) (string, error) {
	var b strings.Builder
	b.WriteString("[[accounts]]\n")
	str := func(key, value string) error {
		if value == "" {
			return nil
		}
		q, err := tomlString(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		fmt.Fprintf(&b, "%s = %s\n", key, q)
		return nil
	}
	num := func(key string, value int) {
		if value != 0 {
			fmt.Fprintf(&b, "%s = %d\n", key, value)
		}
	}
	for _, f := range []struct{ key, value string }{
		{"name", a.Name}, {"email", a.Email}, {"username", a.Username}, {"auth_method", a.AuthMethod},
		{"oauth_client_id", a.OAuthClientID}, {"oauth_client_secret", a.OAuthClientSecret},
	} {
		if err := str(f.key, f.value); err != nil {
			return "", err
		}
	}
	if err := str("imap_host", a.IMAPHost); err != nil {
		return "", err
	}
	num("imap_port", a.IMAPPort)
	if err := str("smtp_host", a.SMTPHost); err != nil {
		return "", err
	}
	num("smtp_port", a.SMTPPort)
	if err := str("tls", a.TLS); err != nil {
		return "", err
	}
	return b.String(), nil
}

// AppendAccount adds an account to the config file, creating the file
// (owner-only) when it does not exist. The block is appended as text: the
// existing file, with the user's comments and layout, is left as it is.
func AppendAccount(a AccountConfig) error {
	block, err := AccountBlock(a)
	if err != nil {
		return err
	}
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// A config that is a symlink (managed by a dotfiles or Nix setup) is
	// not ours to replace.
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; add the account block to its target yourself", path)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out strings.Builder
	out.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		out.WriteString("\n")
	}
	if len(existing) > 0 {
		out.WriteString("\n")
	}
	out.WriteString(block)
	// Write to a temporary file and rename, so a failure cannot leave a
	// half-written config behind.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(out.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// tomlString quotes a value as a TOML basic string. Values come from the
// user and from detected server settings; control characters have no
// business in any of them and are refused rather than escaped.
func tomlString(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("value contains a control character")
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
}
