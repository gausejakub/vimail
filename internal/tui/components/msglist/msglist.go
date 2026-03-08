package msglist

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
)

const pageSize = 500

type Model struct {
	width      int
	height     int
	focused    bool
	store      email.Store
	messages   []email.Message
	loadOffset int  // absolute index of messages[0] in the full folder
	totalCount int  // total messages in folder (for pagination)
	loading    bool // true while async folder load is in progress
	cursor     int  // index into messages slice (local)
	offset     int  // viewport scroll offset (local)
	folder     string
	account    string
	pendingKey string // for multi-key sequences (dd, gg)
	countBuf   string // numeric prefix for commands (e.g. 500gg)

	visualMode   bool
	visualAnchor int

	syncingAccts []string // accounts currently syncing
}

// folderLoadedMsg carries the result of an async folder load.
type folderLoadedMsg struct {
	Account    string
	Folder     string
	Messages   []email.Message
	TotalCount int
}

// jumpLoadedMsg carries the result of an async window load for G/gg jumps.
type jumpLoadedMsg struct {
	Account    string
	Folder     string
	Messages   []email.Message
	LoadOffset int // absolute index of Messages[0]
	Cursor     int // desired local cursor position within Messages
}

// SetSyncing sets syncing state for all or no accounts.
func (m Model) SetSyncing(s bool) Model {
	if !s {
		m.syncingAccts = nil
	}
	return m
}

// SetAccountSyncing sets syncing state for a specific account.
func (m Model) SetAccountSyncing(email string, syncing bool) Model {
	if syncing {
		// Add if not already present.
		for _, e := range m.syncingAccts {
			if e == email {
				return m
			}
		}
		m.syncingAccts = append(append([]string{}, m.syncingAccts...), email)
	} else {
		// Remove by building a new slice (safe for value receiver).
		var filtered []string
		for _, e := range m.syncingAccts {
			if e != email {
				filtered = append(filtered, e)
			}
		}
		m.syncingAccts = filtered
	}
	return m
}

// isSyncing returns true if the current account is still syncing.
func (m Model) isSyncing() bool {
	for _, e := range m.syncingAccts {
		if e == m.account {
			return true
		}
	}
	return false
}

func New(store email.Store) Model {
	accts := store.Accounts()
	var msgs []email.Message
	var acctEmail string
	var total int
	if len(accts) > 0 {
		acctEmail = accts[0].Email
		msgs = store.MessagesForPage(acctEmail, "Inbox", 0, pageSize)
		total = store.MessageCount(acctEmail, "Inbox")
	}
	return Model{
		store:      store,
		messages:   msgs,
		totalCount: total,
		folder:     "Inbox",
		account:    acctEmail,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

// absPos returns the absolute position of the cursor in the full folder.
func (m Model) absPos() int {
	return m.loadOffset + m.cursor
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Width/height are set via SetSize from the layout, not from WindowSizeMsg.
	case util.FolderSelectedMsg:
		m.account = msg.Account
		m.folder = msg.Folder
		m.messages = nil
		m.totalCount = 0
		m.loadOffset = 0
		m.cursor = 0
		m.offset = 0
		m.loading = true
		store := m.store
		acct, folder := msg.Account, msg.Folder
		return m, func() tea.Msg {
			msgs := store.MessagesForPage(acct, folder, 0, pageSize)
			total := store.MessageCount(acct, folder)
			return folderLoadedMsg{Account: acct, Folder: folder, Messages: msgs, TotalCount: total}
		}
	case folderLoadedMsg:
		if msg.Account == m.account && msg.Folder == m.folder {
			m.messages = msg.Messages
			m.totalCount = msg.TotalCount
			m.loadOffset = 0
			m.loading = false
			if len(m.messages) > 0 {
				return m, m.selectCurrent()
			}
		}
	case jumpLoadedMsg:
		if msg.Account == m.account && msg.Folder == m.folder {
			m.messages = msg.Messages
			m.loadOffset = msg.LoadOffset
			m.loading = false
			m.cursor = msg.Cursor
			if m.cursor >= len(m.messages) {
				m.cursor = len(m.messages) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.ensureVisible()
			return m, m.selectCurrent()
		}
	case util.FolderRefreshMsg:
		if msg.Account == m.account && msg.Folder == m.folder {
			m.totalCount = m.store.MessageCount(msg.Account, msg.Folder)
			// Reload the current window.
			m.messages = m.store.MessagesForPage(msg.Account, msg.Folder, m.loadOffset, pageSize)
			if m.cursor >= len(m.messages) && len(m.messages) > 0 {
				m.cursor = len(m.messages) - 1
			}
			m.ensureVisible()
			return m, m.selectCurrent()
		}
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
				m.countBuf = ""
				return m, func() tea.Msg {
					return util.DeleteRequestMsg{
						Account: m.account,
						Folder:  m.folder,
						Message: msg,
					}
				}
			}
			m.countBuf = ""
			return m, nil
		case pending == "g" && key == "g":
			count := m.consumeCount()
			if count > 0 {
				absTarget := count - 1
				return m, m.jumpToAbs(absTarget)
			}
			// gg without count — jump to top.
			return m, m.jumpToAbs(0)
		}
		m.countBuf = ""
		// Pending cancelled; fall through to process this key normally.
	}

	// Accumulate digits for count prefix (e.g. 500gg, 10j).
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		// Don't start with 0 (0 is not a valid line number prefix).
		if m.countBuf != "" || key != "0" {
			m.countBuf += key
			return m, nil
		}
	}

	// No-op on empty list (except pending keys).
	if len(m.messages) == 0 {
		switch key {
		case "d":
			m.pendingKey = "d"
		case "g":
			m.pendingKey = "g"
		default:
			m.countBuf = ""
		}
		return m, nil
	}

	switch key {
	case "j", "down":
		n := m.consumeCount()
		if n == 0 {
			n = 1
		}
		m.cursor += n
		if m.cursor >= len(m.messages) {
			m.cursor = len(m.messages) - 1
		}
		m.loadMoreIfNeeded()
		m.ensureVisible()
	case "k", "up":
		n := m.consumeCount()
		if n == 0 {
			n = 1
		}
		m.cursor -= n
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.loadPreviousIfNeeded()
		m.ensureVisible()
	case "d":
		m.pendingKey = "d"
		return m, nil
	case "g":
		m.pendingKey = "g"
		return m, nil
	case "G":
		count := m.consumeCount()
		if count > 0 {
			return m, m.jumpToAbs(count - 1)
		}
		// G without count — jump to bottom.
		return m, m.jumpToAbs(m.totalCount - 1)
	default:
		m.countBuf = ""
	}
	return m, m.selectCurrent()
}

// jumpToAbs jumps to an absolute position. If it's within the current window,
// moves the cursor directly. Otherwise loads a new window async.
func (m *Model) jumpToAbs(absTarget int) tea.Cmd {
	if absTarget < 0 {
		absTarget = 0
	}
	if absTarget >= m.totalCount {
		absTarget = m.totalCount - 1
	}

	// Check if target is within the loaded window.
	localTarget := absTarget - m.loadOffset
	if localTarget >= 0 && localTarget < len(m.messages) {
		m.cursor = localTarget
		m.ensureVisible()
		return m.selectCurrent()
	}

	// Need to load a new window — do it async.
	m.loading = true
	store := m.store
	acct, folder := m.account, m.folder
	total := m.totalCount
	return func() tea.Msg {
		// Center the window around the target, biased so target is visible.
		windowStart := absTarget - pageSize/2
		if windowStart < 0 {
			windowStart = 0
		}
		if windowStart+pageSize > total {
			windowStart = total - pageSize
			if windowStart < 0 {
				windowStart = 0
			}
		}
		msgs := store.MessagesForPage(acct, folder, windowStart, pageSize)
		cursor := absTarget - windowStart
		return jumpLoadedMsg{
			Account:    acct,
			Folder:     folder,
			Messages:   msgs,
			LoadOffset: windowStart,
			Cursor:     cursor,
		}
	}
}

// consumeCount reads and resets the accumulated numeric count. Returns 0 if none.
func (m *Model) consumeCount() int {
	if m.countBuf == "" {
		return 0
	}
	n := 0
	for _, c := range m.countBuf {
		n = n*10 + int(c-'0')
	}
	m.countBuf = ""
	return n
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

// UpdateBody updates the body and attachments of a message in the list (after lazy fetch).
func (m Model) UpdateBody(uid uint32, body, htmlBody string, attachments []email.Attachment) Model {
	for i := range m.messages {
		if m.messages[i].UID == uid {
			m.messages[i].Body = body
			m.messages[i].HTMLBody = htmlBody
			m.messages[i].Attachments = attachments
			break
		}
	}
	return m
}

// loadMoreIfNeeded loads additional messages when the cursor approaches the end of the window.
func (m *Model) loadMoreIfNeeded() {
	windowEnd := m.loadOffset + len(m.messages)
	if windowEnd >= m.totalCount {
		return // at the end of the folder
	}
	// Load more when within 50 messages of the window end.
	if m.cursor >= len(m.messages)-50 {
		more := m.store.MessagesForPage(m.account, m.folder, windowEnd, pageSize)
		m.messages = append(m.messages, more...)
		// Trim the front if the window gets too large (keep ~3 pages max).
		maxWindow := pageSize * 3
		if len(m.messages) > maxWindow {
			trim := len(m.messages) - maxWindow
			m.messages = m.messages[trim:]
			m.loadOffset += trim
			m.cursor -= trim
			m.offset -= trim
			if m.offset < 0 {
				m.offset = 0
			}
		}
	}
}

// loadPreviousIfNeeded loads previous messages when the cursor approaches the start of the window.
func (m *Model) loadPreviousIfNeeded() {
	if m.loadOffset == 0 {
		return // already at the beginning
	}
	if m.cursor < 50 {
		// Load a page before the current window.
		prevStart := m.loadOffset - pageSize
		if prevStart < 0 {
			prevStart = 0
		}
		count := m.loadOffset - prevStart
		if count <= 0 {
			return
		}
		prev := m.store.MessagesForPage(m.account, m.folder, prevStart, count)
		m.messages = append(prev, m.messages...)
		m.loadOffset = prevStart
		m.cursor += len(prev)
		m.offset += len(prev)
		// Trim the back if the window gets too large.
		maxWindow := pageSize * 3
		if len(m.messages) > maxWindow {
			m.messages = m.messages[:maxWindow]
		}
	}
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
		total := m.totalCount
		if total < m.loadOffset+len(m.messages) {
			total = m.loadOffset + len(m.messages)
		}
		posText = fmt.Sprintf(" %d/%d", m.absPos()+1, total)
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
		// Empty folder state — center text vertically and horizontally.
		emptyLine := fmt.Sprintf("%*s", m.width, "")
		availRows := m.height - 2 // subtract header line
		msg := "No messages"
		if m.loading {
			msg = "Loading…"
		} else if m.isSyncing() {
			msg = "Syncing…"
		}
		topPad := max(0, availRows/2)
		for i := 0; i < topPad; i++ {
			lines = append(lines, emptyLine)
		}
		msgWidth := len([]rune(msg))
		pad := max(0, (m.width-msgWidth)/2)
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
	fromName := sanitize(displayName(msg.From))
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
// displayName extracts the human-readable name from "Name <email>" format.
func displayName(addr string) string {
	if idx := strings.Index(addr, " <"); idx > 0 {
		return addr[:idx]
	}
	return addr
}

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

// MarkReadByID sets the Unread flag to false for the message with the given ID.
func (m Model) MarkReadByID(id string) Model {
	for i := range m.messages {
		if m.messages[i].ID == id {
			m.messages[i].Unread = false
			break
		}
	}
	return m
}

// MarkReadRange marks all messages in the given index range as read.
func (m Model) MarkReadRange(lo, hi int) Model {
	for i := lo; i <= hi && i < len(m.messages); i++ {
		m.messages[i].Unread = false
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

// VisualRange returns the low and high indices of the visual selection.
func (m Model) VisualRange() (int, int) {
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// VisualCoversAll returns true if the visual selection covers the entire loaded
// window and there are more messages in the folder beyond what's loaded.
// This detects the common "select all" pattern (ggVGd) even with a sliding window.
func (m Model) VisualCoversAll() bool {
	if !m.visualMode || m.totalCount == 0 {
		return false
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	// Selection covers the full loaded window.
	return lo == 0 && hi >= len(m.messages)-1
}

// TotalCount returns the total number of messages in the current folder.
func (m Model) TotalCount() int {
	return m.totalCount
}

// SetCursor moves the cursor to the given position, clamped to valid bounds.
func (m Model) SetCursor(pos int) Model {
	if pos < 0 {
		pos = 0
	}
	if pos >= len(m.messages) && len(m.messages) > 0 {
		pos = len(m.messages) - 1
	}
	m.cursor = pos
	m.ensureVisible()
	return m
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
