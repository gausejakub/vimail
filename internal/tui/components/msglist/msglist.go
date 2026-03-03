package msglist

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
)

type Model struct {
	width      int
	height     int
	focused    bool
	store      email.Store
	messages   []email.Message
	cursor     int
	offset     int // viewport scroll offset
	folder     string
	account    string
	pendingKey string // for multi-key sequences (dd, gg)

	visualMode   bool
	visualAnchor int
}

func New(store email.Store) Model {
	accts := store.Accounts()
	var msgs []email.Message
	var acctEmail string
	if len(accts) > 0 {
		acctEmail = accts[0].Email
		msgs = store.MessagesFor(acctEmail, "Inbox")
	}
	return Model{
		store:    store,
		messages: msgs,
		folder:   "Inbox",
		account:  acctEmail,
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
		m.messages = m.store.MessagesFor(msg.Account, msg.Folder)
		m.cursor = 0
		m.offset = 0
	case util.FolderRefreshMsg:
		m.messages = m.store.MessagesFor(msg.Account, msg.Folder)
		if m.cursor >= len(m.messages) && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		}
		m.ensureVisible()
	}
	return m, nil
}

func (m Model) HandleKey(key string) (Model, tea.Cmd) {
	// Handle pending key sequences (dd, gg).
	if m.pendingKey != "" {
		pending := m.pendingKey
		m.pendingKey = ""
		switch {
		case pending == "d" && key == "d":
			if m.cursor < len(m.messages) {
				msg := m.messages[m.cursor]
				return m, func() tea.Msg {
					return util.DeleteRequestMsg{
						Account: m.account,
						Folder:  m.folder,
						Message: msg,
					}
				}
			}
			return m, nil
		case pending == "g" && key == "g":
			m.cursor = 0
			m.ensureVisible()
			return m, m.selectCurrent()
		}
		// Pending cancelled; fall through to process this key normally.
	}

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
	case "d":
		m.pendingKey = "d"
		return m, nil
	case "g":
		m.pendingKey = "g"
		return m, nil
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
		folderText = fmt.Sprintf("* %s (%d)", m.folder, unreadCount)
	}
	posText := ""
	if len(m.messages) > 0 {
		posText = fmt.Sprintf(" %d/%d", m.cursor+1, len(m.messages))
	}
	// Pad plain text to full width, then apply colors to segments.
	plainHeader := folderText + posText
	paddedHeader := padRight(plainHeader, m.width)
	// Re-split into colored segments: folder part + pos part + padding.
	headerLine := lipgloss.NewStyle().Foreground(t.Primary()).Bold(true).Render(folderText)
	if posText != "" {
		headerLine += lipgloss.NewStyle().Foreground(t.TextMuted()).Render(posText)
	}
	padLen := len([]rune(paddedHeader)) - len([]rune(plainHeader))
	if padLen > 0 {
		headerLine += fmt.Sprintf("%*s", padLen, "")
	}
	lines = append(lines, headerLine)

	if len(m.messages) > 0 {
		// Column headers
		colHeader := formatRow("", "From", "Subject", "Time", m.width, t.TextMuted(), t.TextMuted(), t.TextMuted(), t.TextMuted(), lipgloss.Color(""), false)
		lines = append(lines, colHeader)

		// Message rows
		visibleRows := m.height - 2
		for i := m.offset; i < len(m.messages) && i < m.offset+visibleRows; i++ {
			msg := m.messages[i]
			lines = append(lines, m.renderMessage(i, msg))
		}
	} else {
		// Empty folder state
		emptyLine := fmt.Sprintf("%*s", m.width, "")
		topPad := max(0, (m.height-3)/3)
		for i := 0; i < topPad; i++ {
			lines = append(lines, emptyLine)
		}
		// Center "No messages" text
		msg := "No messages"
		pad := (m.width - len(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		centered := fmt.Sprintf("%*s", pad, "") + msg
		centered = padRight(centered, m.width)
		lines = append(lines, lipgloss.NewStyle().Foreground(t.TextMuted()).Render(centered))
	}

	// Pad
	emptyLine := fmt.Sprintf("%*s", m.width, "")
	for len(lines) < m.height {
		lines = append(lines, emptyLine)
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

func (m Model) renderMessage(idx int, msg email.Message) string {
	t := theme.Current()
	isCursor := idx == m.cursor && m.focused

	// Check if this row is within the visual selection range.
	isVisualSelected := false
	if m.visualMode && m.focused {
		lo, hi := m.visualAnchor, m.cursor
		if lo > hi {
			lo, hi = hi, lo
		}
		isVisualSelected = idx >= lo && idx <= hi
	}

	// Determine colors.
	indFg := t.TextMuted()
	fromFg := t.TextMuted()
	subjFg := t.Text()
	timeFg := t.TextMuted()
	bg := lipgloss.Color("")

	indicator := "  "
	if msg.Flagged {
		indicator = "! "
		indFg = t.Warning()
	} else if msg.Unread {
		indicator = "* "
		indFg = t.Primary()
		fromFg = t.TextEmphasized()
		subjFg = t.TextEmphasized()
	}

	if isVisualSelected || isCursor {
		bg = t.Selection()
		indFg = t.SelectionText()
		fromFg = t.SelectionText()
		subjFg = t.SelectionText()
		timeFg = t.SelectionText()
	}

	timeStr := relativeTime(msg.Date)
	fromName := sanitize(msg.From)
	subject := sanitize(msg.Subject)

	return formatRow(indicator, fromName, subject, timeStr, m.width, indFg, fromFg, subjFg, timeFg, bg, msg.Unread)
}

func formatRow(indicator, from, subject, timeStr string, width int, indFg, fromFg, subjFg, timeFg lipgloss.Color, bg lipgloss.Color, bold bool) string {
	indWidth := 2
	timeWidth := 5
	fromWidth := width*28/100 - indWidth
	subjWidth := width - indWidth - fromWidth - timeWidth

	if fromWidth < 6 {
		fromWidth = 6
	}
	if subjWidth < 6 {
		subjWidth = 6
	}

	// Build fixed-width plain strings first, then apply color only (no lipgloss Width).
	indStr := padRight(indicator, indWidth)
	fromStr := padRight(truncate(from, fromWidth), fromWidth)
	subjStr := " " + padRight(truncate(subject, subjWidth-1), subjWidth-1)
	timeStr = padLeft(truncate(timeStr, timeWidth), timeWidth)

	// Apply only color/bold — no Width/MaxWidth.
	style := func(fg lipgloss.Color) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(fg)
		if bg != lipgloss.Color("") {
			s = s.Background(bg)
		}
		return s
	}
	boldStyle := func(fg lipgloss.Color) lipgloss.Style {
		s := style(fg)
		if bold {
			s = s.Bold(true)
		}
		return s
	}

	return style(indFg).Render(indStr) +
		boldStyle(fromFg).Render(fromStr) +
		boldStyle(subjFg).Render(subjStr) +
		style(timeFg).Render(timeStr)
}

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	pad := width - len(r)
	return s + fmt.Sprintf("%*s", pad, "")
}

func padLeft(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	pad := width - len(r)
	return fmt.Sprintf("%*s", pad, "") + s
}

// sanitize strips newlines, carriage returns, tabs, and other control
// characters that would break the fixed-row layout.
func sanitize(s string) string {
	var b []rune
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b = append(b, ' ')
		} else if r >= 0x20 || r == 0 {
			b = append(b, r)
		}
	}
	return string(b)
}

// truncate cuts a string to maxWidth runes.
func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	return string(runes[:maxWidth])
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

func (m Model) SelectedMessage() *email.Message {
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

// MarkCurrentRead sets the current message's Unread flag to false.
func (m Model) MarkCurrentRead() Model {
	if m.cursor < len(m.messages) {
		m.messages[m.cursor].Unread = false
	}
	return m
}

// EnterVisual activates visual mode with the anchor at the current cursor.
func (m Model) EnterVisual() Model {
	m.visualMode = true
	m.visualAnchor = m.cursor
	return m
}

// ExitVisual deactivates visual mode.
func (m Model) ExitVisual() Model {
	m.visualMode = false
	return m
}

// InVisualMode returns whether visual selection is active.
func (m Model) InVisualMode() bool {
	return m.visualMode
}

// SelectedMessages returns the contiguous range of messages between the
// visual anchor and the cursor (inclusive).
func (m Model) SelectedMessages() []email.Message {
	if !m.visualMode || len(m.messages) == 0 {
		return nil
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi >= len(m.messages) {
		hi = len(m.messages) - 1
	}
	result := make([]email.Message, hi-lo+1)
	copy(result, m.messages[lo:hi+1])
	return result
}
