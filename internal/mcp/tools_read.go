package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
)

const (
	defaultPageSize    = 50
	maxPageSize        = 200
	defaultSearchLimit = 50
	maxSearchLimit     = 100000
	defaultRecentLimit = 500
	maxRecentLimit     = 5000
	maxBatchRead       = 50
	defaultBodyChars   = 30000
	maxBodyChars       = 200000
)

// accountInfo is the wire shape of a configured account.
type accountInfo struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type listAccountsArgs struct{}

type listAccountsResult struct {
	Accounts []accountInfo `json:"accounts"`
}

type listFoldersArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
}

type folderInfo struct {
	Name   string `json:"name"`
	Unread int    `json:"unread"`
	Total  int    `json:"total"`
}

type listFoldersResult struct {
	Account string       `json:"account"`
	Folders []folderInfo `json:"folders"`
}

type listOperationsArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"most recent operations to return (default 100, max 1000)"`
}

type operationInfo struct {
	ID            int64  `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Account       string `json:"account"`
	Folder        string `json:"folder,omitempty"`
	Count         int    `json:"count,omitempty"`
	All           bool   `json:"all,omitempty"`
	Destination   string `json:"destination,omitempty"`
	Attempts      int    `json:"attempts"`
	Error         string `json:"error,omitempty"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type listOperationsResult struct {
	Limit      int             `json:"limit"`
	Operations []operationInfo `json:"operations"`
}

type listMessagesArgs struct {
	Account  string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder   string `json:"folder" jsonschema:"folder name, e.g. Inbox"`
	Page     int    `json:"page,omitempty" jsonschema:"zero-based page number (default 0)"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"messages per page (default 50, max 200)"`
}

type messageHeader struct {
	Account string `json:"account,omitempty"`
	UID     uint32 `json:"uid"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Unread  bool   `json:"unread"`
	Flagged bool   `json:"flagged,omitempty"`
	Folder  string `json:"folder,omitempty"`
}

type listMessagesResult struct {
	Account  string          `json:"account"`
	Folder   string          `json:"folder"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int             `json:"total"`
	Messages []messageHeader `json:"messages"`
}

type readMessageArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder  string `json:"folder" jsonschema:"folder name, e.g. Inbox"`
	UID     uint32 `json:"uid" jsonschema:"message UID from list_messages or search_messages"`
}

type readMessageResult struct {
	messageHeader
	Body          string   `json:"body,omitempty"`
	BodyCached    bool     `json:"body_cached"`
	BodyChars     int      `json:"body_chars,omitempty"`
	BodyTruncated bool     `json:"body_truncated,omitempty"`
	Note          string   `json:"note,omitempty"`
	Attachments   []string `json:"attachments,omitempty"`
}

type messageRef struct {
	Account string `json:"account" jsonschema:"account email from list_recent_messages"`
	Folder  string `json:"folder" jsonschema:"folder from list_recent_messages"`
	UID     uint32 `json:"uid" jsonschema:"UID from list_recent_messages"`
}

type readMessagesArgs struct {
	Messages     []messageRef `json:"messages" jsonschema:"message handles to read (max 50)"`
	FetchMissing bool         `json:"fetch_missing,omitempty" jsonschema:"fetch uncached bodies from IMAP without marking messages read; recommended when reviewing message importance"`
	MaxBodyChars int          `json:"max_body_chars,omitempty" jsonschema:"maximum characters returned per body (default 30000, max 200000); use read_message when the complete body is needed"`
}

type readMessageError struct {
	messageRef
	Error string `json:"error"`
}

type readMessagesResult struct {
	Messages []readMessageResult `json:"messages"`
	Errors   []readMessageError  `json:"errors,omitempty"`
}

type listRecentMessagesArgs struct {
	Accounts []string `json:"accounts,omitempty" jsonschema:"account emails to include; omit for every configured account"`
	Since    string   `json:"since" jsonschema:"inclusive RFC3339 start time, e.g. 2026-08-25T00:00:00+02:00"`
	Until    string   `json:"until,omitempty" jsonschema:"exclusive RFC3339 end time; defaults to now"`
	Fresh    bool     `json:"fresh,omitempty" jsonschema:"sync included accounts before listing; sync errors are reported without hiding cached mail"`
	Limit    int      `json:"limit,omitempty" jsonschema:"maximum unique messages (default 500, max 5000)"`
}

type recentSyncResult struct {
	Account     string `json:"account"`
	NewMessages int    `json:"new_messages,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

type listRecentMessagesResult struct {
	Since     string             `json:"since"`
	Until     string             `json:"until"`
	Limit     int                `json:"limit"`
	Truncated bool               `json:"truncated"`
	Sync      []recentSyncResult `json:"sync,omitempty"`
	Messages  []messageHeader    `json:"messages"`
}

type searchMessagesArgs struct {
	Account string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Query   string `json:"query" jsonschema:"text matched against subject, sender, recipients, and cached bodies"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results (default 50)"`
}

type searchMessagesResult struct {
	Account   string          `json:"account"`
	Query     string          `json:"query"`
	Limit     int             `json:"limit"`
	Truncated bool            `json:"truncated"`
	Messages  []messageHeader `json:"messages"`
}

// resolveAccount validates the requested account against the cache, and lets
// single-account setups omit the account argument entirely.
func (s *Server) resolveAccount(requested string) (string, error) {
	accts := s.store.Accounts()
	if len(accts) == 0 {
		return "", fmt.Errorf("no accounts configured — run `vimail setup` first")
	}
	if requested == "" {
		if len(accts) == 1 {
			return accts[0].Email, nil
		}
		return "", fmt.Errorf("multiple accounts configured — pass the account argument (see list_accounts)")
	}
	for _, a := range accts {
		if a.Email == requested {
			return a.Email, nil
		}
	}
	return "", fmt.Errorf("unknown account %q (see list_accounts)", requested)
}

func header(m email.Message, includeFolder bool) messageHeader {
	h := messageHeader{
		Account: m.Account,
		UID:     m.UID,
		From:    m.From,
		To:      m.To,
		Subject: m.Subject,
		Date:    m.Date.Format(time.RFC3339),
		Unread:  m.Unread,
		Flagged: m.Flagged,
	}
	if includeFolder {
		h.Folder = m.Folder
	}
	return h
}

func (s *Server) recentAccounts(requested []string) ([]string, error) {
	configured := s.store.Accounts()
	if len(configured) == 0 {
		return nil, fmt.Errorf("no accounts configured — run `vimail setup` first")
	}
	if len(requested) == 0 {
		accounts := make([]string, 0, len(configured))
		for _, account := range configured {
			accounts = append(accounts, account.Email)
		}
		return accounts, nil
	}

	known := make(map[string]struct{}, len(configured))
	for _, account := range configured {
		known[account.Email] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	accounts := make([]string, 0, len(requested))
	for _, account := range requested {
		if _, ok := known[account]; !ok {
			return nil, fmt.Errorf("unknown account %q (see list_accounts)", account)
		}
		if _, duplicate := seen[account]; duplicate {
			continue
		}
		seen[account] = struct{}{}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (s *Server) readCachedMessage(account, folder string, uid uint32) (readMessageResult, error) {
	if folder == "" {
		return readMessageResult{}, fmt.Errorf("folder is required")
	}
	m, bodyCached, ok := s.store.MessageByUID(account, folder, uid)
	if !ok {
		return readMessageResult{}, fmt.Errorf("message %d not found in %s/%s", uid, account, folder)
	}
	out := readMessageResult{
		messageHeader: header(m, true),
		Body:          m.Body,
		BodyCached:    bodyCached,
	}
	if !bodyCached {
		out.Note = "body not cached yet — use read_messages with fetch_missing=true or open it in the TUI"
	}
	for _, attachment := range m.Attachments {
		out.Attachments = append(out.Attachments, attachment.Filename)
	}
	return out, nil
}

func truncateBody(message *readMessageResult, maxChars int) {
	if message.Body == "" {
		return
	}
	runes := []rune(message.Body)
	message.BodyChars = len(runes)
	if len(runes) > maxChars {
		message.Body = string(runes[:maxChars])
		message.BodyTruncated = true
	}
}

// registerReadTools registers cache-first read tools. Network access is
// explicit through list_recent_messages fresh=true or read_messages
// fetch_missing=true.
func (s *Server) registerReadTools() {
	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "list_recent_messages",
		Description: "List unique received messages in a time window across all accounts in one call. Excludes Sent, Drafts, and Trash; collapses Gmail label copies; set fresh=true to sync every included account first. Use read_messages to inspect selected bodies in one batch.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args listRecentMessagesArgs) (*sdk.CallToolResult, listRecentMessagesResult, error) {
		accounts, err := s.recentAccounts(args.Accounts)
		if err != nil {
			return nil, listRecentMessagesResult{}, err
		}
		if args.Since == "" {
			return nil, listRecentMessagesResult{}, fmt.Errorf("since is required and must be RFC3339")
		}
		since, err := time.Parse(time.RFC3339, args.Since)
		if err != nil {
			return nil, listRecentMessagesResult{}, fmt.Errorf("invalid since time: %w", err)
		}
		until := time.Now()
		if args.Until != "" {
			until, err = time.Parse(time.RFC3339, args.Until)
			if err != nil {
				return nil, listRecentMessagesResult{}, fmt.Errorf("invalid until time: %w", err)
			}
		}
		if !since.Before(until) {
			return nil, listRecentMessagesResult{}, fmt.Errorf("since must be before until")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = defaultRecentLimit
		}
		if limit > maxRecentLimit {
			limit = maxRecentLimit
		}

		out := listRecentMessagesResult{
			Since: since.Format(time.RFC3339), Until: until.Format(time.RFC3339), Limit: limit,
		}
		if args.Fresh {
			if s.coord == nil {
				for _, account := range accounts {
					out.Sync = append(out.Sync, recentSyncResult{Account: account, Error: "sync unavailable: no coordinator configured"})
				}
			} else {
				for _, account := range accounts {
					started := time.Now()
					newCount, syncErr := s.coord.SyncAccountNow(account)
					status := recentSyncResult{Account: account, NewMessages: newCount, DurationMs: time.Since(started).Milliseconds()}
					if syncErr != nil {
						status.Error = syncErr.Error()
					}
					out.Sync = append(out.Sync, status)
				}
			}
		}

		matches := s.store.RecentMessages(accounts, since, until, limit+1)
		if len(matches) > limit {
			out.Truncated = true
			matches = matches[:limit]
		}
		for _, message := range matches {
			out.Messages = append(out.Messages, header(message, true))
		}
		logging.Debug("mcp", "list_recent_messages", logging.KV("accounts", len(accounts)), logging.KV("returned", len(out.Messages)), logging.KV("fresh", args.Fresh), logging.KV("truncated", out.Truncated))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "list_accounts",
		Description: "List the configured email accounts.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args listAccountsArgs) (*sdk.CallToolResult, listAccountsResult, error) {
		var out listAccountsResult
		for _, a := range s.store.Accounts() {
			out.Accounts = append(out.Accounts, accountInfo{Email: a.Email, Name: a.Name})
		}
		logging.Debug("mcp", "list_accounts", logging.KV("count", len(out.Accounts)))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "list_operations",
		Description: "List recent mailbox write operations and their delivery state. Use this to verify whether mark-read, delete, restore, or send operations completed, remain queued/retrying, or failed.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args listOperationsArgs) (*sdk.CallToolResult, listOperationsResult, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}
		out := listOperationsResult{Limit: limit}
		for _, op := range s.store.RecentOps(limit) {
			info := operationInfo{
				ID: op.ID, Type: string(op.Type), Status: string(op.Status),
				Account: op.Account, Folder: op.Folder, Attempts: op.Attempts,
				CreatedAt: op.CreatedAt.Format(time.RFC3339), UpdatedAt: op.UpdatedAt.Format(time.RFC3339),
			}
			if op.Status != cache.OpCompleted {
				info.Error = op.Error
				if !op.NextAttemptAt.IsZero() {
					info.NextAttemptAt = op.NextAttemptAt.Format(time.RFC3339)
				}
			}
			switch op.Type {
			case cache.OpDelete:
				var payload cache.DeletePayload
				if json.Unmarshal(op.Payload, &payload) == nil {
					info.Count = len(payload.UIDs)
				}
			case cache.OpMarkRead:
				var payload cache.MarkReadPayload
				if json.Unmarshal(op.Payload, &payload) == nil {
					info.Count = len(payload.UIDs)
					info.All = payload.All
				}
			case cache.OpRestore:
				var payload cache.RestorePayload
				if json.Unmarshal(op.Payload, &payload) == nil {
					info.Count = len(payload.UIDs)
					info.Destination = payload.Destination
				}
			}
			out.Operations = append(out.Operations, info)
		}
		logging.Debug("mcp", "list_operations", logging.KV("returned", len(out.Operations)))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "list_folders",
		Description: "List an account's folders with unread and total message counts.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args listFoldersArgs) (*sdk.CallToolResult, listFoldersResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, listFoldersResult{}, err
		}
		out := listFoldersResult{Account: acct}
		for _, f := range s.store.FoldersFor(acct) {
			out.Folders = append(out.Folders, folderInfo{
				Name:   f.Name,
				Unread: f.UnreadCount,
				Total:  s.store.MessageCount(acct, f.Name),
			})
		}
		logging.Debug("mcp", "list_folders", logging.Acct(acct), logging.KV("count", len(out.Folders)))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "list_messages",
		Description: "List message headers in a folder, newest first, paged. Data comes from the local cache and is as fresh as the last sync.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args listMessagesArgs) (*sdk.CallToolResult, listMessagesResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, listMessagesResult{}, err
		}
		if args.Folder == "" {
			return nil, listMessagesResult{}, fmt.Errorf("folder is required (see list_folders)")
		}
		page := args.Page
		if page < 0 {
			page = 0
		}
		size := args.PageSize
		if size <= 0 {
			size = defaultPageSize
		}
		if size > maxPageSize {
			size = maxPageSize
		}
		out := listMessagesResult{
			Account:  acct,
			Folder:   args.Folder,
			Page:     page,
			PageSize: size,
			Total:    s.store.MessageCount(acct, args.Folder),
		}
		for _, m := range s.store.MessagesForPage(acct, args.Folder, page*size, size) {
			out.Messages = append(out.Messages, header(m, false))
		}
		logging.Debug("mcp", "list_messages", logging.Acct(acct), logging.Fld(args.Folder), logging.KV("page", page), logging.KV("returned", len(out.Messages)))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "read_message",
		Description: "Read a full message, including its cached body and attachment names. Bodies are fetched lazily by the sync process; body_cached reports whether one is available yet.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args readMessageArgs) (*sdk.CallToolResult, readMessageResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, readMessageResult{}, err
		}
		if args.Folder == "" {
			return nil, readMessageResult{}, fmt.Errorf("folder is required (see list_folders)")
		}
		out, err := s.readCachedMessage(acct, args.Folder, args.UID)
		if err != nil {
			return nil, readMessageResult{}, err
		}
		logging.Debug("mcp", "read_message", logging.Acct(acct), logging.Fld(args.Folder), logging.MsgUID(args.UID), logging.KV("body_cached", out.BodyCached))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "read_messages",
		Description: "Read body excerpts and attachment names for up to 50 message handles in one call. Set fetch_missing=true when details matter; uncached bodies are fetched from IMAP without marking messages read. Reports truncation and per-message errors; use read_message for a complete long body.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args readMessagesArgs) (*sdk.CallToolResult, readMessagesResult, error) {
		if len(args.Messages) == 0 {
			return nil, readMessagesResult{}, fmt.Errorf("messages is required")
		}
		if len(args.Messages) > maxBatchRead {
			return nil, readMessagesResult{}, fmt.Errorf("too many messages: maximum is %d", maxBatchRead)
		}
		bodyChars := args.MaxBodyChars
		if bodyChars <= 0 {
			bodyChars = defaultBodyChars
		}
		if bodyChars > maxBodyChars {
			bodyChars = maxBodyChars
		}
		out := readMessagesResult{}
		for _, ref := range args.Messages {
			account, err := s.resolveAccount(ref.Account)
			if err != nil {
				out.Errors = append(out.Errors, readMessageError{messageRef: ref, Error: err.Error()})
				continue
			}
			message, err := s.readCachedMessage(account, ref.Folder, ref.UID)
			if err != nil {
				out.Errors = append(out.Errors, readMessageError{messageRef: ref, Error: err.Error()})
				continue
			}
			if args.FetchMissing && !message.BodyCached {
				if s.coord == nil {
					message.Note = "body fetch unavailable: no coordinator configured"
					out.Errors = append(out.Errors, readMessageError{messageRef: ref, Error: message.Note})
				} else if _, fetchErr := s.coord.PeekBodyNow(account, ref.Folder, ref.UID); fetchErr != nil {
					message.Note = "body fetch failed: " + fetchErr.Error()
					out.Errors = append(out.Errors, readMessageError{messageRef: ref, Error: fetchErr.Error()})
				} else {
					message, err = s.readCachedMessage(account, ref.Folder, ref.UID)
					if err != nil {
						out.Errors = append(out.Errors, readMessageError{messageRef: ref, Error: err.Error()})
						continue
					}
				}
			}
			truncateBody(&message, bodyChars)
			out.Messages = append(out.Messages, message)
		}
		logging.Debug("mcp", "read_messages", logging.KV("requested", len(args.Messages)), logging.KV("returned", len(out.Messages)), logging.KV("errors", len(out.Errors)), logging.KV("fetch_missing", args.FetchMissing))
		return nil, out, nil
	})

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "search_messages",
		Description: "Search the local cache across subject, sender, recipients, and cached bodies. Results contain real folder/UID handles and report when the requested limit truncated the result set.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, args searchMessagesArgs) (*sdk.CallToolResult, searchMessagesResult, error) {
		acct, err := s.resolveAccount(args.Account)
		if err != nil {
			return nil, searchMessagesResult{}, err
		}
		if args.Query == "" {
			return nil, searchMessagesResult{}, fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = defaultSearchLimit
		}
		if limit > maxSearchLimit {
			limit = maxSearchLimit
		}
		matches := s.store.SearchMessages(acct, args.Query, limit+1)
		out := searchMessagesResult{Account: acct, Query: args.Query, Limit: limit}
		if len(matches) > limit {
			out.Truncated = true
			matches = matches[:limit]
		}
		for _, m := range matches {
			out.Messages = append(out.Messages, header(m, true))
		}
		logging.Debug("mcp", "search_messages", logging.Acct(acct), logging.KV("query", args.Query), logging.KV("returned", len(out.Messages)), logging.KV("truncated", out.Truncated))
		return nil, out, nil
	})
}
