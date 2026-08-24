// Package mcp exposes vmail's local email cache to Model Context Protocol
// clients (Claude Code, Claude Desktop) over stdio.
//
// v1 is read-only and serves entirely from the SQLite cache: tool calls never
// open IMAP connections, which makes them safe alongside a running TUI (WAL
// mode allows the concurrent reads) and functional with no TUI running at
// all. Data is as fresh as the last sync — that staleness is the documented
// contract until an explicit sync tool lands.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
)

// version identifies the vmail MCP server implementation to clients.
const version = "0.1.0"

// Server wraps an MCP server over the vmail SQLite cache.
type Server struct {
	cfg   config.Config
	store *cache.SQLiteStore
	srv   *sdk.Server
}

// New creates an MCP server with the read-only tool set registered.
func New(cfg config.Config, store *cache.SQLiteStore) *Server {
	s := &Server{
		cfg:   cfg,
		store: store,
		srv: sdk.NewServer(&sdk.Implementation{
			Name:    "vmail",
			Title:   "vmail email client",
			Version: version,
		}, nil),
	}
	s.registerReadTools()
	return s
}

// Run serves MCP over stdio until the client disconnects or ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	return s.srv.Run(ctx, &sdk.StdioTransport{})
}

// Connect attaches the server to an arbitrary transport. Tests use this with
// the SDK's in-memory transport pair.
func (s *Server) Connect(ctx context.Context, t sdk.Transport) (*sdk.ServerSession, error) {
	return s.srv.Connect(ctx, t, nil)
}
