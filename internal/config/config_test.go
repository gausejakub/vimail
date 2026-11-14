package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestMCPAllowSendDefaultsFalse(t *testing.T) {
	// Section absent entirely.
	var cfg Config
	if _, err := toml.Decode(`
[general]
preview_pane = true
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.MCP.AllowSend {
		t.Error("allow_send true with [mcp] section absent, want false")
	}

	// Section present but key omitted.
	cfg = Config{}
	if _, err := toml.Decode("[mcp]\n", &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.MCP.AllowSend {
		t.Error("allow_send true with key omitted, want false")
	}
}

func TestOAuthMotionAndVerificationConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.General.Motion {
		t.Fatal("motion should default true")
	}
	if _, err := toml.Decode(`
[general]
motion = false
[sync]
verify_interval = "90m"
[[accounts]]
email = "alice@example.com"
oauth_client_id = "client"
oauth_client_secret = "secret"
`, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.General.Motion || cfg.Accounts[0].OAuthClientID != "client" {
		t.Fatalf("decoded config=%+v", cfg)
	}
	interval, err := cfg.Sync.VerificationInterval()
	if err != nil || interval != 90*time.Minute {
		t.Fatalf("interval=%s err=%v", interval, err)
	}
	cfg.Sync.VerifyInterval = "0"
	interval, err = cfg.Sync.VerificationInterval()
	if err != nil || interval != 0 {
		t.Fatalf("disabled interval=%s err=%v", interval, err)
	}
}

func TestMCPAllowSendOptIn(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode("[mcp]\nallow_send = true\n", &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.MCP.AllowSend {
		t.Error("allow_send = true not decoded")
	}
}

func TestSecretCommandConfig(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[[accounts]]
email = "a@example.com"
password_cmd = "op read 'op://Private/a/password'"

[cache]
key_cmd = "pass show vimail/key"
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := cfg.Accounts[0].PasswordCmd; got != "op read 'op://Private/a/password'" {
		t.Errorf("password_cmd = %q", got)
	}
	if cfg.Cache.KeyCmd != "pass show vimail/key" {
		t.Errorf("key_cmd = %q", cfg.Cache.KeyCmd)
	}
}

func TestSignatureFor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, ".signature"), []byte("From a file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := AccountConfig{
		Email:     "me@x.test",
		Signature: "Default",
		Signatures: map[string]string{
			"Work@Corp.test": "Work sig",
			"file@x.test":    "file:~/.signature",
			"gone@x.test":    "file:~/missing",
		},
	}
	for from, want := range map[string]string{
		"me@x.test":                "Default",
		"Jakub <work@corp.test>":   "Work sig",
		"file@x.test":              "From a file\n",
		"gone@x.test":              "",
		"someone@else.test":        "Default",
		"x <a@x.test>, <b@x.test>": "Default", // not one address: the account's
	} {
		if got := a.SignatureFor(from); got != want {
			t.Errorf("SignatureFor(%q) = %q, want %q", from, got, want)
		}
	}
	if got := (AccountConfig{}).SignatureFor("me@x.test"); got != "" {
		t.Errorf("no signature configured: %q", got)
	}
}
