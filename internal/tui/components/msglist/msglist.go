package msglist

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gause/vmail/internal/mock"
	"github.com/gause/vmail/internal/theme"
	"github.com/gause/vmail/internal/tui/util"
)

type Model struct {
	width    int
	height   int
	focused  bool
	messages []mock.Message
	cursor   int
	offset   int // viewport scroll offset
	folder   string
	account  string
}

func New() Model {
	msgs := mock.MessagesFor(mock.Accounts[0].Email, "Inbox")
	return Model{
		messages: msgs,
		folder:   "Inbox",
		account:  mock.Accounts[0].Email,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case util.FolderSelectedMsg:
		m.account = msg.Account
		m.folder = msg.Folder
		m.messages = mock.MessagesFor(msg.Account, msg.Folder)
		m.cursor = 0
		m.offset = 0
	}
	return m, nil
}

func (m Model) HandleKey(key string) (Model, tea.Cmd) {
	switch key {
	case "j", "down":
		if m.cursor < len(m.messages)-1 {
			m.cursor++
			m.ensureVisible()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case "g":
		m.cursor = 0
		m.ensureVisible()
	case "G":
		if len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
			m.ensureVisible()
		}
	}
	return m, m.selectCurrent()
}

// selectCurrent emits a MessageSelectedMsg for the message under the cursor.
func (m Model) selectCurrent() tea.Cmd {
	if m.cursor < len(m.messages) {
		msg := m.messages[m.cursor]
		return func() tea.Msg {
			return util.MessageSelectedMsg{Message: msg}
		}
	}
	return nil
}

func (m *Model) ensureVisible() {
	visibleRows := m.height - 2 // header + column header
	if visibleRows < 1 {
		visibleRows = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visibleRows {
		m.offset = m.cursor - visibleRows + 1
	}
}

func (m Model) View() string {
	t := theme.Current()
	var lines []string

	// Folder header
	unreadCount := 0
	for _, msg := range m.messages {
		if msg.Unread {
			unreadCount++
		}
	}
	// Folder header with position indicator
	folderText := m.folder
	if unreadCount > 0 {
		folderText = fmt.Sprintf("● %s (%d)", m.folder, unreadCount)
	}
	headerContent := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render(folderText)
	if len(m.messages) > 0 {
		headerContent += lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Render(fmt.Sprintf(" %d/%d", m.cursor+1, len(m.messages)))
	}
	header := lipgloss.NewStyle().Width(m.width).Render(headerContent)
	lines = append(lines, header)

	if len(m.messages) > 0 {
		// Column headers
		colHeader := formatRow("From", "Subject", "Time", m.width, t.TextMuted(), t.TextMuted(), t.TextMuted(), lipgloss.Color(""), false)
		lines = append(lines, colHeader)

		// Message rows
		visibleRows := m.height - 2
		for i := m.offset; i < len(m.messages) && i < m.offset+visibleRows; i++ {
			msg := m.messages[i]
			lines = append(lines, m.renderMessage(i, msg))
		}
	} else {
		// Empty folder state
		topPad := max(0, (m.height-3)/3)
		for i := 0; i < topPad; i++ {
			lines = append(lines, lipgloss.NewStyle().Width(m.width).Render(""))
		}
		lines = append(lines, lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Width(m.width).
			Align(lipgloss.Center).
			Render("No messages"))
	}

	// Pad
	for len(lines) < m.height {
		lines = append(lines, lipgloss.NewStyle().Width(m.width).Render(""))
	}

	result := ""
	for i, line := range lines {
		if i >= m.height {
			break
		}
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func (m Model) renderMessage(idx int, msg mock.Message) string {
	t := theme.Current()
	isCursor := idx == m.cursor && m.focused

	// Determine colors
	fromFg := t.TextMuted()
	subjFg := t.Text()
	timeFg := t.TextMuted()
	bg := lipgloss.Color("")

	if msg.Unread {
		fromFg = t.TextEmphasized()
		subjFg = t.TextEmphasized()
	}

	if isCursor {
		bg = t.Selection()
		fromFg = t.SelectionText()
		subjFg = t.SelectionText()
		timeFg = t.SelectionText()
	}

	prefix := "  "
	if msg.Flagged {
		flagColor := t.Warning()
		if isCursor {
			flagColor = t.SelectionText()
		}
		prefix = lipgloss.NewStyle().Foreground(flagColor).Render("⚑") + " "
	} else if msg.Unread {
		dotColor := t.Primary()
		if isCursor {
			dotColor = t.SelectionText()
		}
		prefix = lipgloss.NewStyle().Foreground(dotColor).Render("●") + " "
	}

	timeStr := relativeTime(msg.Date)

	// Extract display name from "Name <email>" format
	fromName := msg.From
	if idx := strings.Index(fromName, " <"); idx > 0 {
		fromName = fromName[:idx]
	}

	return formatRow(prefix+fromName, msg.Subject, timeStr, m.width, fromFg, subjFg, timeFg, bg, msg.Unread)
}

func formatRow(from, subject, timeStr string, width int, fromFg, subjFg, timeFg lipgloss.Color, bg lipgloss.Color, bold bool) string {
	timeWidth := 5
	fromWidth := width * 28 / 100
	subjWidth := width - fromWidth - timeWidth - 1

	if fromWidth < 8 {
		fromWidth = 8
	}
	if subjWidth < 8 {
		subjWidth = 8
	}

	fromStyle := lipgloss.NewStyle().
		Width(fromWidth).
		MaxWidth(fromWidth).
		MaxHeight(1).
		Foreground(fromFg).
		Bold(bold)
	subjStyle := lipgloss.NewStyle().
		Width(subjWidth).
		MaxWidth(subjWidth).
		MaxHeight(1).
		Foreground(subjFg).
		PaddingLeft(1).
		Bold(bold)
	timeStyle := lipgloss.NewStyle().
		Width(timeWidth).
		MaxWidth(timeWidth).
		MaxHeight(1).
		Foreground(timeFg).
		Align(lipgloss.Right)

	if bg != lipgloss.Color("") {
		fromStyle = fromStyle.Background(bg)
		subjStyle = subjStyle.Background(bg)
		timeStyle = timeStyle.Background(bg)
	}

	return fromStyle.Render(from) + subjStyle.Render(subject) + timeStyle.Render(timeStr)
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m Model) Focus() Model {
	m.focused = true
	return m
}

func (m Model) Blur() Model {
	m.focused = false
	return m
}

func (m Model) SetSize(w, h int) Model {
	m.width = w
	m.height = h
	return m
}

func (m Model) SelectedMessage() *mock.Message {
	if m.cursor < len(m.messages) {
		msg := m.messages[m.cursor]
		return &msg
	}
	return nil
}

func (m Model) CurrentFolder() string {
	return m.folder
}

func (m Model) CurrentAccount() string {
	return m.account
}
