package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
)

func toolNames(t *testing.T, session *sdk.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestSendEmailHiddenByDefault(t *testing.T) {
	// allow_send defaults to false (section absent) — the tool must not be
	// registered at all, not registered-but-erroring.
	session := connect(t, seededStore(t))
	names := toolNames(t, session)
	if names["send_email"] {
		t.Error("send_email registered without [mcp] allow_send = true")
	}

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "send_email", Arguments: map[string]any{"to": "x@example.com"},
	})
	// Calling the unregistered tool must fail one way or the other.
	if err == nil && (res == nil || !res.IsError) {
		t.Error("calling unregistered send_email unexpectedly succeeded")
	}
}

func TestSendEmailRegisteredWhenAllowed(t *testing.T) {
	cfg := config.Config{MCP: config.MCPConfig{AllowSend: true}}
	session := connectCfg(t, seededStore(t), cfg)
	if !toolNames(t, session)["send_email"] {
		t.Error("send_email missing despite allow_send = true")
	}
}

func TestSendFailureLeavesOpRetryable(t *testing.T) {
	store := seededStore(t)
	cfg := config.Config{
		MCP: config.MCPConfig{AllowSend: true},
		Accounts: []config.AccountConfig{{
			Name: "Alice", Email: testAcct,
			// No resolvable credentials and no reachable server: the send
			// must fail cleanly and stay queued.
			SMTPHost: "smtp.invalid.example.com", SMTPPort: 587,
		}},
	}
	session := connectCfg(t, store, cfg)

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "send_email",
		Arguments: map[string]any{
			"to": "bob@example.com", "subject": "hi", "body": "test",
		},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("send against unreachable SMTP unexpectedly succeeded")
	}

	// TUI-consistent failure state: one queued send op, still retryable.
	ops := store.RecentOps(10)
	if len(ops) != 1 || ops[0].Type != cache.OpSend {
		t.Fatalf("queue after failed send = %+v, want one retryable send op", ops)
	}
	if ops[0].Error == "" {
		t.Error("failed send op has no error recorded for the :ops view")
	}
}

func TestConfigAllowSendDefaultsFalse(t *testing.T) {
	var cfg config.Config
	if cfg.MCP.AllowSend {
		t.Error("zero-value config has allow_send enabled")
	}
}
