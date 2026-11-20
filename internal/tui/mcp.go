package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gausejakub/vimail/internal/cache"
	vmcp "github.com/gausejakub/vimail/internal/mcp"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// showMCPMsg opens the MCP overlay with a fresh list of running servers.
type showMCPMsg struct{ instances []vmcp.Instance }

// mcpStartedMsg reports the outcome of :mcp start.
type mcpStartedMsg struct {
	srv *vmcp.HTTPServer
	err error
}

// mcpStoppedMsg reports that :mcp stop closed the TUI's server.
type mcpStoppedMsg struct{ err error }

// StopMCP stops the MCP server :mcp start ran, if any. Call it after the
// program exits.
func (m Model) StopMCP() {
	if m.mcpHTTP != nil {
		_ = m.mcpHTTP.Stop()
	}
}

func listMCPCmd() tea.Cmd {
	return func() tea.Msg { return showMCPMsg{instances: vmcp.ListInstances(vmcp.RegistryDir())} }
}

// mcpCommand handles ":mcp", ":mcp start [port|addr]" and ":mcp stop".
func (m Model) mcpCommand(args []string) tea.Cmd {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status", "ls", "list":
		return listMCPCmd()

	case "start":
		if m.mcpHTTP != nil {
			return listMCPCmd() // already running: show it
		}
		store, ok := m.store.(*cache.SQLiteStore)
		if !ok || m.coordinator == nil {
			return func() tea.Msg {
				return util.InfoMsg{Text: "The MCP server needs a configured account", IsError: true}
			}
		}
		addr := m.cfg.MCP.HTTPAddr
		if addr == "" {
			addr = vmcp.DefaultHTTPAddr
		}
		if len(args) > 1 {
			addr = args[1]
			if _, err := strconv.Atoi(addr); err == nil {
				addr = "127.0.0.1:" + addr
			}
		}
		cfg, coord := m.cfg, m.coordinator
		return func() tea.Msg {
			tok, err := vmcp.LoadOrCreateToken(filepath.Dir(vmcp.RegistryDir()))
			if err != nil {
				return mcpStartedMsg{err: err}
			}
			srv, err := vmcp.New(cfg, store, coord).StartHTTP(addr, tok)
			return mcpStartedMsg{srv: srv, err: err}
		}

	case "stop":
		srv := m.mcpHTTP
		if srv == nil {
			return func() tea.Msg { return util.InfoMsg{Text: "No MCP server started from vimail", IsError: true} }
		}
		return func() tea.Msg { return mcpStoppedMsg{err: srv.Stop()} }
	}
	return func() tea.Msg {
		return util.InfoMsg{Text: "Usage: :mcp [start [port]|stop]", IsError: true}
	}
}

func (m Model) mcpView() string {
	t := theme.Current()
	title := lipgloss.NewStyle().Foreground(t.Primary()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	info := lipgloss.NewStyle().Foreground(t.Info())
	warn := lipgloss.NewStyle().Foreground(t.Warning())

	rows := []string{title.Render("  MCP Servers"), ""}
	if len(m.mcpInstances) == 0 {
		rows = append(rows, muted.Render("  None running. :mcp start serves one over HTTP."))
	}
	for _, in := range m.mcpInstances {
		line := fmt.Sprintf("  ● %s  pid %d  up %s", in.Transport, in.PID, formatUptime(time.Since(in.StartedAt)))
		if m.mcpHTTP != nil && in.Addr == m.mcpHTTP.URL {
			line += "  (this vimail)"
		}
		rows = append(rows, info.Render(line))
		if in.Addr != "" {
			rows = append(rows, "      "+in.Addr)
		}
		perm := "read only"
		if in.AllowWrite {
			perm = "read + write"
		}
		if in.AllowSend {
			perm += " + send"
		}
		clients := "no client yet"
		if len(in.Clients) > 0 {
			clients = strings.Join(in.Clients, ", ")
		}
		permStyle := muted
		if in.AllowSend {
			permStyle = warn
		}
		rows = append(rows, permStyle.Render("      "+perm+"  ·  "+clients))
	}

	if m.mcpHTTP != nil {
		rows = append(rows, "", title.Render("  Connect Claude Code"),
			"  claude mcp add --transport http vimail "+m.mcpHTTP.URL+` \`,
			`    --header "Authorization: Bearer `+m.mcpHTTP.Token+`"`,
			"", muted.Render("  :mcp stop stops it. It also stops when vimail quits."))
	}

	rows = append(rows, "", muted.Render("  Press Esc or q to close"))
	content := strings.Join(rows, "\n")
	w := lipgloss.Width(content) + 6
	if m.width > 0 && w > m.width-4 {
		w = m.width - 4
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(w).
		Render(content)
}

func formatUptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
