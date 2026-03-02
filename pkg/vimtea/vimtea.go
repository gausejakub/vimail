/*
Package vimtea provides a Vim-like text editor component for terminal applications
built with Bubble Tea (github.com/charmbracelet/bubbletea).

# Features

  - Vim-like modal editing with normal, insert, visual, and command modes
  - Familiar key bindings for Vim users (h,j,k,l navigation, d/y/p for delete/yank/paste, etc.)
  - Command mode with colon commands
  - Visual mode for selecting text
  - Undo/redo functionality
  - Line numbers (regular and relative)
  - Syntax highlighting
  - Customizable styles and themes
  - Extensible key binding system

# Getting Started

Create a new editor with default settings:

	editor := vimtea.NewEditor()

Or customize it with options:

	editor := vimtea.NewEditor(
		vimtea.WithContent("Initial content"),
		vimtea.WithEnableStatusBar(true),
		vimtea.WithDefaultSyntaxTheme("catppuccin-macchiato"),
		vimtea.WithRelativeNumbers(true),
	)

Use it in a Bubble Tea application:

	p := tea.NewProgram(editor)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}

# Extending Functionality

Add custom key bindings:

	editor.AddBinding(vimtea.KeyBinding{
		Key:         "ctrl+s",
		Mode:        vimtea.ModeNormal,
		Description: "Save file",
		Handler: func(buf vimtea.Buffer) tea.Cmd {
			// Your save logic here
			return nil
		},
	})

Add custom command:

	editor.AddCommand("write", func(buf vimtea.Buffer, args []string) tea.Cmd {
		// Your save logic here
		return nil
	})

# Styling

Customize the appearance with style options:

	customStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#333333"))

	editor := vimtea.NewEditor(
		vimtea.WithTextStyle(customStyle),
		vimtea.WithLineNumberStyle(numberStyle),
		vimtea.WithCursorStyle(cursorStyle),
	)
*/
package vimtea
