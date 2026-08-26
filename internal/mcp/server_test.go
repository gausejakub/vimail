package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/worker"
)

const testAcct = "alice@example.com"

// seededStore builds a cache with one account, an Inbox with five messages
// (UIDs 1..5, message 3 unread with a fetched body), and a Sent folder.
func seededStore(t *testing.T) *cache.SQLiteStore {
	t.Helper()
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := cache.NewSQLiteStore(db)

	if err := s.SeedAccount("Alice", testAcct, "imap.example.com", 993, "smtp.example.com", 587); err != nil {
		t.Fatalf("SeedAccount: %v", err)
	}
	for _, f := range []string{"Inbox", "Sent"} {
		if _, err := s.EnsureFolder(testAcct, f); err != nil {
			t.Fatalf("EnsureFolder(%s): %v", f, err)
		}
	}
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for uid := uint32(1); uid <= 5; uid++ {
		msg := email.Message{
			UID:     uid,
			From:    fmt.Sprintf("sender%d@example.com", uid),
			To:      testAcct,
			Subject: fmt.Sprintf("Subject %d", uid),
			Date:    base.Add(time.Duration(uid) * time.Hour),
			Unread:  uid == 3,
		}
		if err := s.UpsertMessage(testAcct, "Inbox", msg); err != nil {
			t.Fatalf("UpsertMessage(%d): %v", uid, err)
		}
	}
	if err := s.UpdateMessageBody(testAcct, "Inbox", 3, "the quarterly report is attached", "", nil); err != nil {
		t.Fatalf("UpdateMessageBody: %v", err)
	}
	return s
}

// connect starts the server on an in-memory transport and returns a live
// client session.
func connect(t *testing.T, store *cache.SQLiteStore) *sdk.ClientSession {
	return connectCfg(t, store, config.Config{})
}

// connectCfg is connect with a caller-supplied config (e.g. MCP options).
func connectCfg(t *testing.T, store *cache.SQLiteStore, cfg config.Config) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := New(cfg, store, worker.NewCoordinator(cfg, store))

	serverT, clientT := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// call invokes a tool and decodes its structured content into out.
func call(t *testing.T, session *sdk.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s returned tool error: %v", tool, res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshal structured content: %v", tool, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: unmarshal into %T: %v", tool, out, err)
	}
}

func TestListTools(t *testing.T) {
	session := connect(t, seededStore(t))

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"delete_draft", "delete_message",
		"list_accounts", "list_folders", "list_messages",
		"mark_read", "read_message", "save_draft", "search_messages", "sync",
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("tool list = %v, want %v", names, want)
	}
}

func TestListAccounts(t *testing.T) {
	session := connect(t, seededStore(t))

	var out listAccountsResult
	call(t, session, "list_accounts", nil, &out)
	if len(out.Accounts) != 1 || out.Accounts[0].Email != testAcct || out.Accounts[0].Name != "Alice" {
		t.Errorf("accounts = %+v, want [Alice %s]", out.Accounts, testAcct)
	}
}

func TestListFoldersCounts(t *testing.T) {
	session := connect(t, seededStore(t))

	var out listFoldersResult
	call(t, session, "list_folders", map[string]any{"account": testAcct}, &out)

	byName := map[string]folderInfo{}
	for _, f := range out.Folders {
		byName[f.Name] = f
	}
	inbox, ok := byName["Inbox"]
	if !ok {
		t.Fatalf("Inbox missing from folders: %+v", out.Folders)
	}
	if inbox.Total != 5 || inbox.Unread != 1 {
		t.Errorf("Inbox = total %d unread %d, want total 5 unread 1", inbox.Total, inbox.Unread)
	}
	if sent, ok := byName["Sent"]; !ok || sent.Total != 0 {
		t.Errorf("Sent = %+v, want present with total 0", sent)
	}
}

func TestListMessagesPagination(t *testing.T) {
	session := connect(t, seededStore(t))

	var page0 listMessagesResult
	call(t, session, "list_messages", map[string]any{"folder": "Inbox", "page": 0, "page_size": 2}, &page0)
	if page0.Total != 5 || len(page0.Messages) != 2 {
		t.Fatalf("page 0 = total %d len %d, want total 5 len 2", page0.Total, len(page0.Messages))
	}
	// Newest first: UID 5 has the latest date.
	if page0.Messages[0].UID != 5 || page0.Messages[1].UID != 4 {
		t.Errorf("page 0 UIDs = %d,%d, want 5,4", page0.Messages[0].UID, page0.Messages[1].UID)
	}

	var page2 listMessagesResult
	call(t, session, "list_messages", map[string]any{"folder": "Inbox", "page": 2, "page_size": 2}, &page2)
	if len(page2.Messages) != 1 || page2.Messages[0].UID != 1 {
		t.Errorf("page 2 = %+v, want single message UID 1", page2.Messages)
	}
	// Account omitted: resolved because exactly one account is configured.
	if page0.Account != testAcct {
		t.Errorf("resolved account = %q, want %q", page0.Account, testAcct)
	}
}

func TestReadMessage(t *testing.T) {
	session := connect(t, seededStore(t))

	var out readMessageResult
	call(t, session, "read_message", map[string]any{"folder": "Inbox", "uid": 3}, &out)
	if out.Body != "the quarterly report is attached" || !out.BodyCached {
		t.Errorf("body = %q cached=%v, want fetched body with body_cached true", out.Body, out.BodyCached)
	}
	if out.Subject != "Subject 3" || !out.Unread {
		t.Errorf("header = %+v, want Subject 3 / unread", out.messageHeader)
	}

	// A message without a fetched body reports body_cached=false and a note.
	var nobody readMessageResult
	call(t, session, "read_message", map[string]any{"folder": "Inbox", "uid": 1}, &nobody)
	if nobody.BodyCached || nobody.Note == "" {
		t.Errorf("uid 1 = cached=%v note=%q, want uncached with explanatory note", nobody.BodyCached, nobody.Note)
	}
}

func TestSearchMessages(t *testing.T) {
	session := connect(t, seededStore(t))

	var out searchMessagesResult
	call(t, session, "search_messages", map[string]any{"query": "quarterly report"}, &out)
	if len(out.Messages) != 1 || out.Messages[0].UID != 3 {
		t.Fatalf("search = %+v, want single hit UID 3", out.Messages)
	}
	if out.Messages[0].Folder == "" {
		t.Error("search hit missing folder context")
	}
}

func TestToolErrors(t *testing.T) {
	session := connect(t, seededStore(t))

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"list_folders", map[string]any{"account": "nobody@example.com"}},
		{"list_messages", map[string]any{}}, // folder missing
		{"read_message", map[string]any{"folder": "Inbox", "uid": 999}},
		{"search_messages", map[string]any{"query": ""}},
	}
	for _, tc := range cases {
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: transport error: %v", tc.tool, err)
		}
		if !res.IsError {
			t.Errorf("%s(%v): expected tool error, got success", tc.tool, tc.args)
		}
	}
}
