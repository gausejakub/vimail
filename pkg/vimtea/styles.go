// Package vimtea provides a Vim-like text editor component for terminal applications
package vimtea

import "github.com/charmbracelet/lipgloss"

// Default styles for the editor components
// These can be overridden using the With* option functions
var (
	// lineNumberStyle defines the appearance of regular line numbers
	lineNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "242"}).
			Bold(false).
			PaddingRight(1)

	// currentLineNumberStyle defines the appearance of the current line number
	currentLineNumberStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "15"}).
				Bold(true).
				Background(lipgloss.AdaptiveColor{Light: "252", Dark: "236"}).
				PaddingRight(1)

	// textStyle defines the appearance of regular text in the editor
	textStyle = lipgloss.NewStyle()

	// statusStyle defines the appearance of the status bar
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "7", Dark: "8"}).
			Background(lipgloss.AdaptiveColor{Light: "8", Dark: "7"})

	// cursorStyle defines the appearance of the cursor
	cursorStyle = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "252", Dark: "248"}).
			Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"})

	// commandStyle defines the appearance of the command line
	commandStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "3", Dark: "3"}).
			Bold(true)

	// selectedStyle defines the appearance of selected text in visual mode
	selectedStyle = lipgloss.NewStyle().Background(
		lipgloss.AdaptiveColor{Light: "7", Dark: "8"},
	)
)
