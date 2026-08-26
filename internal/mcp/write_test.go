package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
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
	if out.UID != 3 || out.Count != 1 || len(out.UIDs) != 1 || out.UIDs[0] != 3 {
		t.Errorf("single mark_read result = %+v, want UID 3 / count 1", out)
	}

	m, _, ok := store.MessageByUID(testAcct, "Inbox", 3)
	if !ok || m.Unread {
		t.Errorf("message 3 after mark_read = unread=%v, want read", m.Unread)
	}

	// The IMAP op is queued; with no connection it failed but stays
	// retryable — the same state a TUI mark-read leaves when offline.
	ops := store.RecentOps(10)
	if len(ops) != 1 || ops[0].Type != cache.OpMarkRead || ops[0].Folder != "Inbox" {
		t.Fatalf("queue after mark_read = %+v, want one retryable mark_read op", ops)
	}

	expectToolError(t, session, "mark_read", map[string]any{"folder": "Inbox", "uid": 999})
}

func TestMarkReadBulkUsesOneQueuedOperation(t *testing.T) {
	store := seededStore(t)
	for _, uid := range []uint32{2, 4} {
		msg, _, _ := store.MessageByUID(testAcct, "Inbox", uid)
		msg.Unread = true
		if err := store.UpsertMessage(testAcct, "Inbox", msg); err != nil {
			t.Fatalf("make UID %d unread: %v", uid, err)
		}
	}
	session := connect(t, store)

	var out markReadResult
	call(t, session, "mark_read", map[string]any{"folder": "Inbox", "uids": []uint32{2, 3, 4, 3}}, &out)
	if out.Count != 3 || len(out.UIDs) != 3 {
		t.Fatalf("bulk result = %+v, want 3 deduplicated UIDs", out)
	}
	for _, uid := range []uint32{2, 3, 4} {
		msg, _, ok := store.MessageByUID(testAcct, "Inbox", uid)
		if !ok || msg.Unread {
			t.Errorf("UID %d after bulk mark_read = found %v unread %v, want read", uid, ok, msg.Unread)
		}
	}

	ops := store.RecentOps(10)
	if len(ops) != 1 || ops[0].Type != cache.OpMarkRead {
		t.Fatalf("queue = %+v, want one mark_read op", ops)
	}
	var payload cache.MarkReadPayload
	if err := json.Unmarshal(ops[0].Payload, &payload); err != nil {
		t.Fatalf("decode queued payload: %v", err)
	}
	if len(payload.UIDs) != 3 {
		t.Fatalf("queued UIDs = %v, want one 3-UID payload", payload.UIDs)
	}
}

func TestMarkReadBulkValidatesBeforeMutation(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)
	expectToolError(t, session, "mark_read", map[string]any{"folder": "Inbox", "uids": []uint32{3, 999}})

	msg, _, ok := store.MessageByUID(testAcct, "Inbox", 3)
	if !ok || !msg.Unread {
		t.Fatal("valid UID was mutated before the invalid batch member failed")
	}
	if ops := store.RecentOps(10); len(ops) != 0 {
		t.Fatalf("invalid batch queued ops: %+v", ops)
	}
	expectToolError(t, session, "mark_read", map[string]any{"folder": "Inbox", "uid": 3, "uids": []uint32{3}})
	expectToolError(t, session, "mark_read", map[string]any{"folder": "Inbox", "uids": []uint32{}})
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
	ops := store.RecentOps(10)
	if len(ops) != 1 || ops[0].Type != cache.OpDelete {
		t.Fatalf("queue after delete_message = %+v, want one retryable delete op", ops)
	}

	// No expunge path: Trash and Drafts are refused.
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Trash", "uid": 1})
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Drafts", "uid": 1})
	expectToolError(t, session, "delete_message", map[string]any{"folder": "Inbox", "uid": 999})
}

func TestDeleteMessageBulkUsesOneQueuedOperation(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)

	var out deleteMessageResult
	call(t, session, "delete_message", map[string]any{"folder": "Inbox", "uids": []uint32{1, 2, 1}}, &out)
	if out.Count != 2 || len(out.UIDs) != 2 {
		t.Fatalf("bulk delete result = %+v, want 2 deduplicated UIDs", out)
	}
	for _, uid := range []uint32{1, 2} {
		if _, _, ok := store.MessageByUID(testAcct, "Inbox", uid); ok {
			t.Errorf("UID %d remains in cache after bulk delete", uid)
		}
	}
	tombstones := store.PendingDeletes()
	if len(tombstones) != 1 || len(tombstones[0].UIDs) != 2 {
		t.Fatalf("tombstones = %+v, want one 2-UID group", tombstones)
	}
	ops := store.RecentOps(10)
	if len(ops) != 1 || ops[0].Type != cache.OpDelete {
		t.Fatalf("queue = %+v, want one delete op", ops)
	}
	var payload cache.DeletePayload
	if err := json.Unmarshal(ops[0].Payload, &payload); err != nil {
		t.Fatalf("decode queued payload: %v", err)
	}
	if len(payload.UIDs) != 2 {
		t.Fatalf("queued UIDs = %v, want one 2-UID payload", payload.UIDs)
	}
}

func TestRestoreMessagesQueuesWithoutConnectionAndKeepsCache(t *testing.T) {
	store := seededStore(t)
	if _, err := store.EnsureFolder(testAcct, "Trash"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessage(testAcct, "Trash", email.Message{
		UID: 77, MessageID: "<restore@example.com>", From: "sender@example.com",
		To: testAcct, Subject: "Restore me", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	session := connect(t, store)

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "restore_messages",
		Arguments: map[string]any{
			"account": testAcct, "uids": []uint32{77}, "destination": "Inbox",
		},
	})
	if err != nil {
		t.Fatalf("restore_messages transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("restore_messages returned tool error: %v", res.Content)
	}
	var out restoreMessagesResult
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Queued || out.ServerUpdated || out.CacheUpdated || out.Requested != 1 || out.Delivered != 0 || out.OperationID == 0 {
		t.Fatalf("restore result = %+v", out)
	}
	if _, _, ok := store.MessageByUID(testAcct, "Trash", 77); !ok {
		t.Fatal("offline restore removed the Trash cache row before server success")
	}
	ops := store.RecentOps(10)
	if len(ops) != 1 || string(ops[0].Type) != "restore" {
		t.Fatalf("queue = %+v, want one retryable restore op", ops)
	}
	var payload cache.RestorePayload
	if err := json.Unmarshal(ops[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.UIDs) != 1 || payload.UIDs[0] != 77 || payload.Destination != "Inbox" {
		t.Fatalf("restore payload = %+v", payload)
	}
}

func TestRestoreMessagesValidatesWholeBatchBeforeQueue(t *testing.T) {
	store := seededStore(t)
	if _, err := store.EnsureFolder(testAcct, "Trash"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessage(testAcct, "Trash", email.Message{
		UID: 77, From: "sender@example.com", To: testAcct, Subject: "Restore me", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	session := connect(t, store)
	expectToolError(t, session, "restore_messages", map[string]any{"uids": []uint32{77, 999}})
	if ops := store.RecentOps(10); len(ops) != 0 {
		t.Fatalf("invalid restore queued operations: %+v", ops)
	}
}

func TestSyncToolReportsFailureCleanly(t *testing.T) {
	// The test coordinator has no account config, so sync must surface a
	// clear error instead of succeeding silently.
	session := connect(t, seededStore(t))
	expectToolError(t, session, "sync", map[string]any{})
}

func TestSyncToolReportsLockContention(t *testing.T) {
	store := seededStore(t)
	release, ok := store.TryAcquireSyncLock(testAcct)
	if !ok {
		t.Fatal("acquire setup sync lock")
	}
	defer release()

	cfg := config.Config{Accounts: []config.AccountConfig{{
		Email: testAcct, IMAPHost: "imap.example.com",
	}}}
	session := connectCfg(t, store, cfg)
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
	opsTUI := storeTUI.RecentOps(10)
	if len(opsTUI) != 1 || opsTUI[0].Type != cache.OpDelete {
		t.Fatalf("TUI store queue = %+v, want the MCP-issued delete op", opsTUI)
	}

	// The failed immediate attempt is backed off. Make it due, then prove
	// only one of the two processes can claim it.
	id := opsTUI[0].ID
	if _, err := dbMCP.Exec(`UPDATE pending_ops SET next_attempt_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339), id); err != nil {
		t.Fatalf("make op retry due: %v", err)
	}
	tuiClaim := storeTUI.StartOp(id)
	mcpClaim := storeMCP.StartOp(id)
	if tuiClaim == mcpClaim {
		t.Errorf("claims: tui=%v mcp=%v, want exactly one winner", tuiClaim, mcpClaim)
	}
}
