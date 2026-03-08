package keys

import "github.com/charmbracelet/bubbles/key"

// Mode represents the current input mode (Vim-style).
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeCommand
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "NORMAL"
	case ModeInsert:
		return "INSERT"
	case ModeVisual:
		return "VISUAL"
	case ModeCommand:
		return "COMMAND"
	default:
		return "UNKNOWN"
	}
}

// ModeChangedMsg is sent when the input mode changes.
type ModeChangedMsg struct {
	Mode Mode
}

// NormalKeyMap defines keybindings available in normal mode.
// h/l are handled at app level for pane switching — not defined here.
type NormalKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Enter    key.Binding
	Quit     key.Binding
	Help     key.Binding
	Command  key.Binding
	Visual   key.Binding
	NextPane key.Binding
	PrevPane key.Binding
	Reply    key.Binding
	Forward  key.Binding
	Delete   key.Binding
	Compose  key.Binding
	Refresh  key.Binding
	Escape   key.Binding
	GoTop    key.Binding
	GoBottom key.Binding
	Search   key.Binding
}

// CommandKeyMap defines keybindings in command mode.
type CommandKeyMap struct {
	Submit key.Binding
	Cancel key.Binding
}

// VisualKeyMap defines keybindings in visual mode.
type VisualKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Toggle key.Binding
	Cancel key.Binding
}

var Normal = NormalKeyMap{
	Up:       key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:     key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
	Quit:     key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Command:  key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
	Visual:   key.NewBinding(key.WithKeys("v", "V"), key.WithHelp("v/V", "visual mode")),
	NextPane: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next pane")),
	PrevPane: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev pane")),
	Reply:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reply")),
	Forward:  key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "forward")),
	Delete:   key.NewBinding(key.WithKeys("d"), key.WithHelp("dd", "delete")),
	Compose:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "compose")),
	Refresh:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
	Escape:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "escape")),
	GoTop:    key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "go to top")),
	GoBottom: key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "go to bottom")),
	Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
}

var Command = CommandKeyMap{
	Submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit")),
	Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

var Visual = VisualKeyMap{
	Up:     key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:   key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	Toggle: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
	Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "exit visual")),
}
