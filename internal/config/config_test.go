package config

import (
	"testing"

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

func TestMCPAllowSendOptIn(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode("[mcp]\nallow_send = true\n", &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.MCP.AllowSend {
		t.Error("allow_send = true not decoded")
	}
}
