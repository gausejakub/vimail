package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
)

func TestEveryToolIsClassifiedAndAnnotated(t *testing.T) {
	session := connectCfg(t, seededStore(t), config.Config{MCP: config.MCPConfig{AllowSend: true, AllowWrite: true}})
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		kind, ok := toolKinds[tool.Name]
		if !ok {
			t.Errorf("tool %q has no entry in toolKinds", tool.Name)
			continue
		}
		if tool.Annotations == nil {
			t.Errorf("tool %q has no annotations", tool.Name)
			continue
		}
		if got := tool.Annotations.ReadOnlyHint; got != (kind == toolRead) {
			t.Errorf("tool %q readOnlyHint = %v", tool.Name, got)
		}
		if kind == toolDestructive && (tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint) {
			t.Errorf("tool %q not marked destructive", tool.Name)
		}
		if returnsMail[tool.Name] && !strings.Contains(tool.Description, "untrusted") {
			t.Errorf("tool %q description lacks the untrusted-content note", tool.Name)
		}
	}
}

func TestDefaultConfigExposesNoStateChangingTools(t *testing.T) {
	session := connectCfg(t, seededStore(t), config.DefaultConfig())
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		if toolKinds[tool.Name] >= toolWrite {
			t.Errorf("default config exposes %q", tool.Name)
		}
	}
	for _, want := range []string{"read_message", "search_messages", "sync", "verify"} {
		if !seen[want] {
			t.Errorf("default config is missing %q", want)
		}
	}
}
