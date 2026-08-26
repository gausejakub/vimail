package mcp

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	"github.com/gausejakub/vimail/internal/worker"
)

type saveDraftArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	ID      string `json:"id,omitempty" jsonschema:"draft id to update; omit to create a new draft"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`
}

type saveDraftResult struct {
	Account string `json:"account"`
	ID      string `json:"id"`
}

type deleteDraftArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	ID      string `json:"id" jsonschema:"draft id from list_messages on the Drafts folder"`
}

type deleteDraftResult struct {
	Account string `json:"account"`
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type markReadArgs struct {
	Account string   `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string   `json:"folder" jsonschema:"folder name, e.g. Inbox"`
	UID     uint32   `json:"uid,omitempty" jsonschema:"one message UID; use either uid or uids"`
	UIDs    []uint32 `json:"uids,omitempty" jsonschema:"message UIDs from list_messages or search_messages; use either uid or uids"`
}

type markReadResult struct {
	Account string   `json:"account"`
	Folder  string   `json:"folder"`
	UID     uint32   `json:"uid,omitempty"`
	UIDs    []uint32 `json:"uids"`
	Count   int      `json:"count"`
	Note    string   `json:"note"`
}

type deleteMessageArgs struct {
	Account string   `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string   `json:"folder" jsonschema:"folder the message lives in; Trash and Drafts are not allowed"`
	UID     uint32   `json:"uid,omitempty" jsonschema:"one message UID; use either uid or uids"`
	UIDs    []uint32 `json:"uids,omitempty" jsonschema:"message UIDs from list_messages or search_messages; use either uid or uids"`
}

type deleteMessageResult struct {
	Account string   `json:"account"`
	Folder  string   `json:"folder"`
	UID     uint32   `json:"uid,omitempty"`
	UIDs    []uint32 `json:"uids"`
	Count   int      `json:"count"`
	Note    string   `json:"note"`
}

type restoreMessagesArgs struct {
	Account     string   `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	UID         uint32   `json:"uid,omitempty" jsonschema:"one Trash UID; use either uid or uids"`
	UIDs        []uint32 `json:"uids,omitempty" jsonschema:"Trash UIDs from list_messages or search_messages; use either uid or uids"`
	Destination string   `json:"destination,omitempty" jsonschema:"folder to restore into (default Inbox)"`
}

type restoreMessagesResult struct {
	Account       string   `json:"account"`
	Source        string   `json:"source"`
	Destination   string   `json:"destination"`
	UIDs          []uint32 `json:"uids"`
	Requested     int      `json:"requested"`
	Delivered     int      `json:"delivered"`
	ServerUpdated bool     `json:"server_updated"`
	CacheUpdated  bool     `json:"cache_updated"`
	Queued        bool     `json:"queued"`
	OperationID   int64    `json:"operation_id,omitempty"`
	Note          string   `json:"note"`
}

type syncArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string `json:"folder,omitempty" jsonschema:"sync only this folder; omit to sync the whole account"`
	Full    bool   `json:"full,omitempty" jsonschema:"authoritatively rebuild cached headers to reconcile server-side moves/deletes; slower than incremental sync"`
}

type syncToolResult struct {
	Account         string `json:"account"`
	Folder          string `json:"folder,omitempty"`
	NewMessages     int    `json:"new_messages"`
	MessagesFetched int    `json:"messages_fetched,omitempty"`
	DurationMs      int64  `json:"duration_ms"`
	Full            bool   `json:"full"`
}

// resolveCreds resolves account credentials once, on first use, so server
// startup never blocks on keyring access.
func (s *Server) resolveCreds() {
	s.credsOnce.Do(func() {
		for _, err := range s.coord.ResolveCredentials() {
			logging.Warn("mcp", "credential resolution failed", logging.Err(err))
		}
	})
}

// registerWriteTools registers the safe write tool set plus the explicit
// sync tool. Writes follow the exact TUI pattern: optimistic cache write
// plus a queued op; the op executes now when a connection exists, otherwise
// it stays queued and retryable, and either process (TUI or this server)
// delivers it on its next sync — exactly once, thanks to op claiming.
func (s *Server) registerWriteTools() {
	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "save_draft",
		Description: "Create a new draft, or update an existing one when id is given. Drafts are stored locally (no server round trip).",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args saveDraftArgs) (*sdk.CallToolResult, saveDraftResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, saveDraftResult{}, err
		}
		id := args.ID
		if id == "" {
			// Not store.NextDraftID(): that per-process counter restarts at 1
			// and would collide with (and overwrite) the TUI's drafts. A
			// timestamp id is unique across both processes.
			id = fmt.Sprintf("draft-mcp-%d", time.Now().UnixNano())
		} else if !s.draftExists(acct, id) {
			return nil, saveDraftResult{}, fmt.Errorf("draft %q not found for %s", id, acct)
		}
		s.store.SaveDraft(acct, email.Message{
			ID:      id,
			From:    acct,
			To:      args.To,
			Subject: args.Subject,
			Body:    args.Body,
			Date:    time.Now(),
		})
		logging.Info("mcp", "draft saved", logging.Acct(acct), logging.KV("draft_id", id))
		return nil, saveDraftResult{Account: acct, ID: id}, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "delete_draft",
		Description: "Delete a local draft by id.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args deleteDraftArgs) (*sdk.CallToolResult, deleteDraftResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, deleteDraftResult{}, err
		}
		if args.ID == "" {
			return nil, deleteDraftResult{}, fmt.Errorf("id is required")
		}
		if !s.draftExists(acct, args.ID) {
			return nil, deleteDraftResult{}, fmt.Errorf("draft %q not found for %s", args.ID, acct)
		}
		s.store.DeleteDraft(acct, args.ID)
		logging.Info("mcp", "draft deleted", logging.Acct(acct), logging.KV("draft_id", args.ID))
		return nil, deleteDraftResult{Account: acct, ID: args.ID, Deleted: true}, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mark_read",
		Description: "Mark one or many messages as read. Pass either uid or uids. One batch updates the cache immediately and creates one queued server operation.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args markReadArgs) (*sdk.CallToolResult, markReadResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, markReadResult{}, err
		}
		uids, err := normalizeUIDs(args.UID, args.UIDs)
		if err != nil {
			return nil, markReadResult{}, err
		}
		if err := validateMessageUIDs(s.store, acct, args.Folder, uids); err != nil {
			return nil, markReadResult{}, err
		}
		// Validate the whole batch before the transactional cache update, then
		// queue and attempt one IMAP operation.
		if err := s.store.MarkReadUIDs(acct, args.Folder, uids); err != nil {
			return nil, markReadResult{}, err
		}
		if s.coord != nil {
			s.coord.MarkReadMessages(acct, args.Folder, uids)()
		}
		logging.Info("mcp", "mark_read", logging.Acct(acct), logging.Fld(args.Folder), logging.KV("count", len(uids)))
		out := markReadResult{
			Account: acct, Folder: args.Folder, UIDs: uids, Count: len(uids),
			Note: "marked read in cache; server flag queued (delivered on next sync if offline)",
		}
		if len(uids) == 1 {
			out.UID = uids[0]
		}
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "delete_message",
		Description: "Move one or many messages to Trash. Pass either uid or uids. Never expunges; one batch creates one queued server operation.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args deleteMessageArgs) (*sdk.CallToolResult, deleteMessageResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, deleteMessageResult{}, err
		}
		switch args.Folder {
		case "Trash":
			return nil, deleteMessageResult{}, fmt.Errorf("permanent deletion from Trash is not available over MCP — use the TUI")
		case "Drafts":
			return nil, deleteMessageResult{}, fmt.Errorf("use delete_draft for drafts")
		}
		uids, err := normalizeUIDs(args.UID, args.UIDs)
		if err != nil {
			return nil, deleteMessageResult{}, err
		}
		ids, err := messageIDsForUIDs(s.store, acct, args.Folder, uids)
		if err != nil {
			return nil, deleteMessageResult{}, err
		}
		// Validate the whole batch before creating tombstones/removing cache
		// rows, then queue and attempt one IMAP move.
		s.store.DeleteMessages(acct, args.Folder, ids)
		if s.coord != nil {
			s.coord.DeleteMessages(acct, args.Folder, uids)()
		}
		logging.Info("mcp", "delete_message", logging.Acct(acct), logging.Fld(args.Folder), logging.KV("count", len(uids)))
		out := deleteMessageResult{
			Account: acct, Folder: args.Folder, UIDs: uids, Count: len(uids),
			Note: "moved to Trash in cache; server move queued (delivered on next sync if offline)",
		}
		if len(uids) == 1 {
			out.UID = uids[0]
		}
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "restore_messages",
		Description: "Restore one or many messages from Trash to Inbox (or another folder). Validates the whole batch, connects lazily, queues offline failures, and reconciles cache rows with server-assigned destination UIDs.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args restoreMessagesArgs) (*sdk.CallToolResult, restoreMessagesResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, restoreMessagesResult{}, err
		}
		uids, err := normalizeUIDs(args.UID, args.UIDs)
		if err != nil {
			return nil, restoreMessagesResult{}, err
		}
		if err := validateMessageUIDs(s.store, acct, "Trash", uids); err != nil {
			return nil, restoreMessagesResult{}, err
		}
		destination := args.Destination
		if destination == "" || destination == "INBOX" {
			destination = "Inbox"
		}
		if destination == "Trash" {
			return nil, restoreMessagesResult{}, fmt.Errorf("destination must differ from Trash")
		}
		if !folderExists(s.store, acct, destination) {
			return nil, restoreMessagesResult{}, fmt.Errorf("destination folder %q not found for %s (see list_folders)", destination, acct)
		}
		if s.coord == nil {
			return nil, restoreMessagesResult{}, fmt.Errorf("restore is unavailable: no coordinator configured")
		}

		restoreMsg := s.coord.RestoreFromTrash(acct, uids, destination)()
		restore, ok := restoreMsg.(worker.RestoreResult)
		if !ok {
			return nil, restoreMessagesResult{}, fmt.Errorf("restore returned an unexpected result")
		}
		out := restoreMessagesResult{
			Account: acct, Source: "Trash", Destination: destination, UIDs: uids,
			Requested: len(uids), Delivered: restore.Count,
			ServerUpdated: restore.Delivered, CacheUpdated: restore.Cached,
			Queued: !restore.Delivered, OperationID: restore.OpID,
		}
		switch {
		case restore.Delivered && restore.Cached:
			out.Note = "restored on server and reconciled in cache"
		case restore.Delivered:
			out.Note = "restored on server; cache refresh needs attention"
			if restore.Err != nil {
				out.Note += ": " + restore.Err.Error()
			}
		default:
			out.Note = "restore queued for retry; messages remain visible in Trash until server delivery"
			if restore.Err != nil {
				out.Note += ": " + restore.Err.Error()
			}
		}
		logging.Info("mcp", "restore_messages", logging.Acct(acct), logging.Fld(destination), logging.KV("requested", len(uids)), logging.KV("delivered", restore.Count), logging.KV("queued", out.Queued))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "sync",
		Description: "Sync an account (or a single folder) with the IMAP server now. Whole-account sync delivers queued writes first. Set full=true to rebuild cached headers authoritatively after server-side moves or deletes.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args syncArgs) (*sdk.CallToolResult, syncToolResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, syncToolResult{}, err
		}
		if s.coord == nil {
			return nil, syncToolResult{}, fmt.Errorf("sync is unavailable: no coordinator configured")
		}
		s.resolveCreds()

		start := time.Now()
		var newCount int
		if args.Folder == "" {
			if args.Full {
				newCount, err = s.coord.SyncAccountFullNow(acct)
			} else {
				newCount, err = s.coord.SyncAccountNow(acct)
			}
		} else {
			if !folderExists(s.store, acct, args.Folder) {
				return nil, syncToolResult{}, fmt.Errorf("folder %q not found for %s (see list_folders)", args.Folder, acct)
			}
			if args.Full {
				newCount, err = s.coord.SyncFolderFullNow(acct, args.Folder)
			} else {
				newCount, err = s.coord.SyncFolderNow(acct, args.Folder)
			}
		}
		if err != nil {
			return nil, syncToolResult{}, fmt.Errorf("sync failed: %w", err)
		}
		logging.Info("mcp", "sync complete", logging.Acct(acct), logging.Fld(args.Folder), logging.KV("new", newCount), logging.Dur(time.Since(start)))
		out := syncToolResult{
			Account:    acct,
			Folder:     args.Folder,
			DurationMs: time.Since(start).Milliseconds(),
			Full:       args.Full,
		}
		if args.Full {
			out.MessagesFetched = newCount
		} else {
			out.NewMessages = newCount
		}
		return nil, out, nil
	})
}

// draftExists reports whether a draft with the given id exists for the account.
func (s *Server) draftExists(acct, id string) bool {
	for _, d := range s.store.MessagesForPage(acct, "Drafts", 0, 0) {
		if d.ID == id {
			return true
		}
	}
	return false
}

func normalizeUIDs(uid uint32, bulk []uint32) ([]uint32, error) {
	if uid != 0 && len(bulk) > 0 {
		return nil, fmt.Errorf("pass either uid or uids, not both")
	}
	uids := bulk
	if uid != 0 {
		uids = []uint32{uid}
	}
	if len(uids) == 0 {
		return nil, fmt.Errorf("uid or uids is required")
	}

	seen := make(map[uint32]struct{}, len(uids))
	normalized := make([]uint32, 0, len(uids))
	for _, candidate := range uids {
		if candidate == 0 {
			return nil, fmt.Errorf("UIDs must be greater than zero")
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized, nil
}

func validateMessageUIDs(store *cache.SQLiteStore, account, folder string, uids []uint32) error {
	_, err := messageIDsForUIDs(store, account, folder, uids)
	return err
}

func messageIDsForUIDs(store *cache.SQLiteStore, account, folder string, uids []uint32) ([]string, error) {
	ids := make([]string, 0, len(uids))
	for _, uid := range uids {
		msg, _, ok := store.MessageByUID(account, folder, uid)
		if !ok {
			return nil, fmt.Errorf("message %d not found in %s/%s", uid, account, folder)
		}
		ids = append(ids, msg.ID)
	}
	return ids, nil
}

func folderExists(store *cache.SQLiteStore, account, folder string) bool {
	for _, candidate := range store.FoldersFor(account) {
		if candidate.Name == folder {
			return true
		}
	}
	return false
}
