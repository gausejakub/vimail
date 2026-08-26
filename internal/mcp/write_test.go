package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
)

// expectToolError asserts a call returns an in-band tool error.
func expectToolError(t *testing.T, session *sdk.ClientSession, tool string, args map[string]any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	if !res.IsError {
		t.Fatalf("%s(%v): expected tool error, got success", tool, args)
	}
}

func TestDraftLifecycle(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)

	// Create.
	var created saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"to": "bob@example.com", "subject": "hello", "body": "first version",
	}, &created)
	if created.ID == "" {
		t.Fatal("save_draft returned empty id")
	}

	// Visible in the Drafts folder, like a TUI-saved draft.
	drafts := store.MessagesForPage(testAcct, "Drafts", 0, 50)
	if len(drafts) != 1 || drafts[0].Subject != "hello" || drafts[0].Body != "first version" {
		t.Fatalf("drafts after create = %+v, want one draft 'hello'", drafts)
	}

	// Update in place by id.
	var updated saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"id": created.ID, "to": "bob@example.com", "subject": "hello", "body": "second version",
	}, &updated)
	if updated.ID != created.ID {
		t.Errorf("update changed draft id %q → %q", created.ID, updated.ID)
	}
	drafts = store.MessagesForPage(testAcct, "Drafts", 0, 50)
	if len(drafts) != 1 || drafts[0].Body != "second version" {
		t.Fatalf("drafts after update = %+v, want single updated draft", drafts)
	}

	// Updating a nonexistent id is an error, not a silent create.
	expectToolError(t, session, "save_draft", map[string]any{"id": "draft-nope", "body": "x"})

	// Delete.
	var deleted deleteDraftResult
	call(t, session, "delete_draft", map[string]any{"id": created.ID}, &deleted)
	if !deleted.Deleted {
		t.Error("delete_draft did not report deletion")
	}
	if drafts := store.MessagesForPage(testAcct, "Drafts", 0, 50); len(drafts) != 0 {
		t.Errorf("drafts after delete = %+v, want none", drafts)
	}
	expectToolError(t, session, "delete_draft", map[string]any{"id": created.ID})
}

func TestMarkReadCacheAndQueue(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)

	// UID 3 is the unread seeded message.
	var out markReadResult
	call(t, session, "mark_read", map[string]any{"folder": "Inbox", "uid": 3}, &out)

	m, _, ok := store.MessageByUID(testAcct, "Inbox", 3)
	if !ok || m.Unread {
		t.Errorf("message 3 after mark_read = unread=%v, want read", m.Unread)
	}

	// The IMAP op is queued; with no connection it failed but stays
	// retryable — the same state a TUI mark-read leaves when offline.
	ops := store.PendingOps()
	if len(ops) != 1 || ops[0].Type != cache.OpMarkRead || ops[0].Folder != "Inbox" {
		t.Fatalf("queue after mark_read = %+v, want one retryable mark_read op", ops)
	}

	expectToolError(t, session, "mark_read", map[string]any{"folder": "Inbox", "uid": 999})
}

func TestDeleteMessageMovesToTrashOnly(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)

	var out deleteMessageResult
	call(t, session, "delete_message", map[string]any{"folder": "Inbox", "uid": 2}, &out)

	// Gone from the cache view, tombstoned, and queued for the server.
	if _, _, ok := store.MessageByUID(testAcct, "Inbox", 2); ok {
		t.Error("message 2 still in cache after delete_message")
	}
	tombstones := store.PendingDeletes()
	if len(tombstones) != 1 || len(tombstones[0].UIDs) != 1 || tombstones[0].UIDs[0] != 2 {
		t.Errorf("tombstones = %+v, want UID 2", tombstones)
	}
	ops := store.PendingOps()
	if len(ops) != 1 || ops[0].Type != cache.OpDelete {
		t.Fatalf("queue after delete_message = %+v, want one retryable delete op", ops)
	}

	// No expunge path: Trash and Drafts are refused.
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Trash", "uid": 1})
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Drafts", "uid": 1})
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Inbox", "uid": 999})
}

func TestSyncToolReportsFailureCleanly(t *testing.T) {
	// The test coordinator has no account config, so sync must surface a
	// clear error instead of succeeding silently.
	session := connect(t, seededStore(t))
	expectToolError(t, session, "sync", map[string]any{})
}

// TestTwoProcessWritePath proves an op written through the MCP server's
// store is visible to — and claimable exactly once by — a second store on
// the same database file, i.e. the running-TUI scenario.
func TestTwoProcessWritePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	dbMCP, err := cache.Open(path)
	if err != nil {
		t.Fatalf("open mcp db: %v", err)
	}
	t.Cleanup(func() { dbMCP.Close() })
	dbTUI, err := cache.Open(path)
	if err != nil {
		t.Fatalf("open tui db: %v", err)
	}
	t.Cleanup(func() { dbTUI.Close() })

	storeMCP := cache.NewSQLiteStore(dbMCP)
	storeTUI := cache.NewSQLiteStore(dbTUI)

	if err := storeMCP.SeedAccount("Alice", testAcct, "imap.example.com", 993, "", 587); err != nil {
		t.Fatalf("SeedAccount: %v", err)
	}
	if _, err := storeMCP.EnsureFolder(testAcct, "Inbox"); err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if err := storeMCP.UpsertMessage(testAcct, "Inbox", email.Message{
		UID: 1, From: "x@example.com", To: testAcct, Subject: "s", Date: time.Now(), Unread: true,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	session := connect(t, storeMCP)
	var out deleteMessageResult
	call(t, session, "delete_message", map[string]any{"folder": "Inbox", "uid": 1}, &out)

	// The TUI process sees the queued op…
	opsTUI := storeTUI.PendingOps()
	if len(opsTUI) != 1 || opsTUI[0].Type != cache.OpDelete {
		t.Fatalf("TUI store queue = %+v, want the MCP-issued delete op", opsTUI)
	}

	// …and only one of the two processes can claim it.
	id := opsTUI[0].ID
	tuiClaim := storeTUI.StartOp(id)
	mcpClaim := storeMCP.StartOp(id)
	if tuiClaim == mcpClaim {
		t.Errorf("claims: tui=%v mcp=%v, want exactly one winner", tuiClaim, mcpClaim)
	}
}
