package mcp

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
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
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string `json:"folder" jsonschema:"folder name, e.g. Inbox"`
	UID     uint32 `json:"uid" jsonschema:"message UID from list_messages or search_messages"`
}

type markReadResult struct {
	Account string `json:"account"`
	Folder  string `json:"folder"`
	UID     uint32 `json:"uid"`
	Note    string `json:"note"`
}

type deleteMessageArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string `json:"folder" jsonschema:"folder the message lives in; Trash and Drafts are not allowed"`
	UID     uint32 `json:"uid" jsonschema:"message UID from list_messages or search_messages"`
}

type deleteMessageResult struct {
	Account string `json:"account"`
	Folder  string `json:"folder"`
	UID     uint32 `json:"uid"`
	Note    string `json:"note"`
}

type syncArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string `json:"folder,omitempty" jsonschema:"sync only this folder; omit to sync the whole account"`
}

type syncToolResult struct {
	Account     string `json:"account"`
	Folder      string `json:"folder,omitempty"`
	NewMessages int    `json:"new_messages"`
	DurationMs  int64  `json:"duration_ms"`
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
		Description: "Mark a message as read. The cache updates immediately; the server-side \\Seen flag is queued and delivered on the next connection or sync.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args markReadArgs) (*sdk.CallToolResult, markReadResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, markReadResult{}, err
		}
		m, _, ok := s.store.MessageByUID(acct, args.Folder, args.UID)
		if !ok {
			return nil, markReadResult{}, fmt.Errorf("message %d not found in %s/%s", args.UID, acct, args.Folder)
		}
		// Same path as the TUI: optimistic cache write (with cross-folder
		// read-state cascade), then queue + attempt the IMAP op.
		s.store.MarkRead(acct, args.Folder, m.ID)
		if s.coord != nil {
			s.coord.MarkRead(acct, args.Folder, args.UID)()
		}
		logging.Info("mcp", "mark_read", logging.Acct(acct), logging.Fld(args.Folder), logging.MsgUID(args.UID))
		return nil, markReadResult{
			Account: acct, Folder: args.Folder, UID: args.UID,
			Note: "marked read in cache; server flag queued (delivered on next sync if offline)",
		}, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "delete_message",
		Description: "Move a message to Trash. Never expunges — deleted messages stay restorable from the TUI. The cache updates immediately; the server-side move is queued and delivered on the next connection or sync.",
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
		m, _, ok := s.store.MessageByUID(acct, args.Folder, args.UID)
		if !ok {
			return nil, deleteMessageResult{}, fmt.Errorf("message %d not found in %s/%s", args.UID, acct, args.Folder)
		}
		// Same path as the TUI: tombstone + cache removal, then queue +
		// attempt the IMAP move-to-Trash.
		s.store.DeleteMessage(acct, args.Folder, m.ID)
		if s.coord != nil {
			s.coord.DeleteMessage(acct, args.Folder, args.UID)()
		}
		logging.Info("mcp", "delete_message", logging.Acct(acct), logging.Fld(args.Folder), logging.MsgUID(args.UID))
		return nil, deleteMessageResult{
			Account: acct, Folder: args.Folder, UID: args.UID,
			Note: "moved to Trash in cache; server move queued (delivered on next sync if offline)",
		}, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "sync",
		Description: "Sync an account (or a single folder) with the IMAP server now, delivering any queued writes first. Reads are cache-first, so call this when fresh data matters.",
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
			newCount, err = s.coord.SyncAccountNow(acct)
		} else {
			newCount, err = s.coord.SyncFolderNow(acct, args.Folder)
		}
		if err != nil {
			return nil, syncToolResult{}, fmt.Errorf("sync failed: %w", err)
		}
		logging.Info("mcp", "sync complete", logging.Acct(acct), logging.Fld(args.Folder), logging.KV("new", newCount), logging.Dur(time.Since(start)))
		return nil, syncToolResult{
			Account:     acct,
			Folder:      args.Folder,
			NewMessages: newCount,
			DurationMs:  time.Since(start).Milliseconds(),
		}, nil
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
