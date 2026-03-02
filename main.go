package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gause/vmail/internal/config"
	"github.com/gause/vmail/internal/tui"

	// Import theme package to trigger init() registrations.
	_ "github.com/gause/vmail/internal/theme"
)

func main() {
	// Check for truecolor support.
	ct := os.Getenv("COLORTERM")
	if ct != "truecolor" && ct != "24bit" {
		fmt.Fprintln(os.Stderr, "vmail: $COLORTERM not set to truecolor — colors may be degraded")
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vmail: failed to load config: %v\n", err)
		os.Exit(1)
	}

	m := tui.New(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "vmail: %v\n", err)
		os.Exit(1)
	}
}
