package config

import (
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
