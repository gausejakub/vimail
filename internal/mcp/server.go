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
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/worker"
)

// version identifies the vmail MCP server implementation to clients.
const version = "0.1.0"

// Server wraps an MCP server over the vmail SQLite cache.
type Server struct {
	cfg   config.Config
	store *cache.SQLiteStore
	coord *worker.Coordinator // drains queued ops and syncs when running standalone
	srv   *sdk.Server

	// credsOnce lazily resolves account credentials on the first sync call,
	// so starting the server never blocks on keyring access.
	credsOnce sync.Once
}

// New creates an MCP server with the read and write tool sets registered.
// coord is the server's own coordinator, used by the sync tool and for
// pushing queued writes; the op queue's cross-process claiming (owner +
// lease) keeps it safe alongside a running TUI.
func New(cfg config.Config, store *cache.SQLiteStore, coord *worker.Coordinator) *Server {
	s := &Server{
		cfg:   cfg,
		store: store,
		coord: coord,
		srv: sdk.NewServer(&sdk.Implementation{
			Name:    "vmail",
			Title:   "vmail email client",
			Version: version,
		}, nil),
	}
	s.registerReadTools()
	s.registerWriteTools()
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
