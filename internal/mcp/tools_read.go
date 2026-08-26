package mcp

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
)

const (
	defaultPageSize    = 50
	maxPageSize        = 200
	defaultSearchLimit = 50
	maxSearchLimit     = 100000
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

type listMessagesArgs struct {
	Account  string `json:"account,omitempty" jsonschema:"account email; may be omitted when exactly one account is configured"`
	Folder   string `json:"folder" jsonschema:"folder name, e.g. Inbox"`
	Page     int    `json:"page,omitempty" jsonschema:"zero-based page number (default 0)"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"messages per page (default 50, max 200)"`
}

type messageHeader struct {
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
	Body        string   `json:"body,omitempty"`
	BodyCached  bool     `json:"body_cached"`
	Note        string   `json:"note,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
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

// registerReadTools registers the read-only v1 tool set. Every handler reads
// from the SQLite cache only — never from the network.
func (s *Server) registerReadTools() {
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
		m, bodyCached, ok := s.store.MessageByUID(acct, args.Folder, args.UID)
		if !ok {
			return nil, readMessageResult{}, fmt.Errorf("message %d not found in %s/%s", args.UID, acct, args.Folder)
		}
		out := readMessageResult{
			messageHeader: header(m, true),
			Body:          m.Body,
			BodyCached:    bodyCached,
		}
		if !bodyCached {
			out.Note = "body not cached yet — it is fetched lazily when the message is opened in the TUI or once a sync tool is available"
		}
		for _, a := range m.Attachments {
			out.Attachments = append(out.Attachments, a.Filename)
		}
		logging.Debug("mcp", "read_message", logging.Acct(acct), logging.Fld(args.Folder), logging.MsgUID(args.UID), logging.KV("body_cached", bodyCached))
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
