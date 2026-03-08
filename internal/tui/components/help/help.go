package help

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/theme"
)

type binding struct {
	key  string
	desc string
}

const sectionMarker = "\x00"

var bindings = []binding{
	{"Navigation", sectionMarker},
	{"j/k", "Up/down in current pane"},
	{"h/l", "Switch pane left/right"},
	{"g/G", "Jump to top/bottom"},
	{"Ctrl+D/U", "Half-page scroll (preview)"},
	{"Tab", "Next pane"},
	{"Shift+Tab", "Previous pane"},
	{"", ""},
	{"Actions", sectionMarker},
	{"c", "Compose new message"},
	{"r", "Reply to message"},
	{"f", "Forward"},
	{"dd", "Delete message"},
	{"u", "Restore from Trash"},
	{"E", "Export message(s) to ZIP"},
	{"R", "Refresh"},
	{"Ctrl+S", "Send (in compose)"},
	{"", ""},
	{"Modes", sectionMarker},
	{":", "Command mode"},
	{"/", "Search messages"},
	{"v", "Visual select (d to delete)"},
	{"?", "Toggle help"},
	{"q", "Quit"},
	{"", ""},
	{"Commands", sectionMarker},
	{":theme <name>", "Switch theme"},
	{":search <query>", "Search messages"},
	{":quit", "Quit vimail"},
}

// View renders the help dialog content.
func View(maxWidth int) string {
	t := theme.Current()

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  vimail — Keyboard Shortcuts")

	sectionStyle := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Bold(true)

	keyStyle := lipgloss.NewStyle().
		Foreground(t.Accent()).
		Bold(true).
		Width(16)

	descStyle := lipgloss.NewStyle().
		Foreground(t.Text())

	var rows []string
	rows = append(rows, title)
	rows = append(rows, "")

	for _, b := range bindings {
		if b.key == "" && b.desc == "" {
			rows = append(rows, "")
			continue
		}
		if b.desc == sectionMarker {
			rows = append(rows, "  "+sectionStyle.Render("── "+b.key+" ──"))
			continue
		}
		rows = append(rows, "  "+keyStyle.Render(b.key)+descStyle.Render(b.desc))
	}

	rows = append(rows, "")
	rows = append(rows, lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render("  Press ? or Esc to close"))

	content := strings.Join(rows, "\n")

	contentWidth := 48
	if maxWidth > 0 && contentWidth > maxWidth-4 {
		contentWidth = maxWidth - 4
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(contentWidth).
		Render(content)
}
