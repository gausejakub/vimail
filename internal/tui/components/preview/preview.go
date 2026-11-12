package preview

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/pkg/vimtea"
	"github.com/muesli/reflow/wordwrap"
)

type Model struct {
	width        int
	height       int
	focused      bool
	message      *email.Message
	scrollOffset int
	pendingOpen  bool // open in browser once body arrives

	// reader is a read-only Vim buffer over the message (Enter in the preview
	// pane): motions, search, visual selection and yank to the clipboard.
	reader  vimtea.Editor
	reading bool
}

func New() Model {
	return Model{}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if ms := m.maxScroll(); m.scrollOffset > ms {
			m.scrollOffset = ms
		}
	case util.MessageSelectedMsg:
		m.message = &msg.Message
		m.scrollOffset = 0
		m.pendingOpen = false
		m = m.StopReading()
	case util.FetchBodyCompleteMsg:
		if m.message != nil && m.message.UID == msg.UID {
			m.message.Body = msg.Body
			m.message.HTMLBody = msg.HTMLBody
			m.message.Attachments = msg.Attachments
			m.scrollOffset = 0
			if m.pendingOpen {
				m.pendingOpen = false
				return m, m.openInBrowser()
			}
		}
	}
	return m, nil
}

func (m Model) HandleKey(key string) (Model, tea.Cmd) {
	if m.message == nil {
		return m, nil
	}
	ms := m.maxScroll()
	switch key {
	case "j", "down":
		if m.scrollOffset < ms {
			m.scrollOffset++
		}
	case "k", "up":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
	case "G":
		m.scrollOffset = ms
	case "g":
		m.scrollOffset = 0
	case "ctrl+d":
		m.scrollOffset = min(m.scrollOffset+m.height/2, ms)
	case "ctrl+u":
		m.scrollOffset = max(0, m.scrollOffset-m.height/2)
	case "o":
		if m.message.Body == "" && m.message.HTMLBody == "" {
			// Body still loading — open once it arrives.
			m.pendingOpen = true
			return m, func() tea.Msg {
				return util.InfoMsg{Text: "Opening after load…", IsError: false}
			}
		}
		return m, m.openInBrowser()
	}
	return m, nil
}

var (
	tempOnce sync.Once
	tempPath string
	tempErr  error
)

// tempDir returns a private (0700, unpredictable name) directory for this
// process. A fixed path such as /tmp/vimail could be pre-created or
// symlinked by another local user.
func tempDir() (string, error) {
	tempOnce.Do(func() { tempPath, tempErr = os.MkdirTemp("", "vimail-") })
	return tempPath, tempErr
}

// CleanupTemp removes any decrypted HTML written for the browser.
func CleanupTemp() {
	if tempPath != "" {
		os.RemoveAll(tempPath)
	}
}

// openInBrowser writes the HTML body to a temp file and opens it.
// Falls back to wrapping plain text in HTML when no HTML part exists.
func (m Model) openInBrowser() tea.Cmd {
	if m.message == nil || (m.message.HTMLBody == "" && m.message.Body == "") {
		return func() tea.Msg {
			return util.InfoMsg{Text: "No content to open", IsError: false}
		}
	}

	// Never let the mail's own markup run scripts or load remote content
	// (tracking pixels, redirects to custom URL schemes) in the browser.
	const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src data:">` + "\n"
	content := m.message.HTMLBody
	if content != "" {
		content = csp + content
	}
	if content == "" {
		// Wrap plain text in minimal HTML for browser viewing.
		escaped := html.EscapeString(m.message.Body)
		content = fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui,sans-serif;max-width:48em;margin:2em auto;padding:0 1em;line-height:1.6;color:#222}
.header{color:#666;border-bottom:1px solid #ddd;padding-bottom:1em;margin-bottom:1em}</style></head>
<body><div class="header"><strong>From:</strong> %s<br><strong>To:</strong> %s<br><strong>Subject:</strong> %s</div>
<pre style="white-space:pre-wrap;word-wrap:break-word">%s</pre></body></html>`,
			html.EscapeString(m.message.Subject),
			html.EscapeString(m.message.From),
			html.EscapeString(m.message.To),
			html.EscapeString(m.message.Subject),
			escaped)
	}

	// Use UID as temp filename — safe integer, no path traversal risk.
	msgID := fmt.Sprintf("%d", m.message.UID)

	return func() tea.Msg {
		dir, err := tempDir()
		if err != nil {
			return util.InfoMsg{Text: "Failed to create temp dir: " + err.Error(), IsError: true}
		}
		path := filepath.Join(dir, fmt.Sprintf("msg-%s.html", msgID))
		_ = os.Remove(path) // our own earlier copy; the dir is private to this process
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.WriteString(content)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			return util.InfoMsg{Text: "Failed to write HTML: " + err.Error(), IsError: true}
		}
		opener := "open"
		if runtime.GOOS != "darwin" {
			opener = "xdg-open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			return util.InfoMsg{Text: "Failed to open browser: " + err.Error(), IsError: true}
		}
		return util.InfoMsg{Text: "Opened in browser", IsError: false}
	}
}

func (m Model) View() string {
	if m.reading && m.reader != nil {
		return m.readerView()
	}
	t := theme.Current()

	if m.message == nil {
		emptyLine := fmt.Sprintf("%*s", m.width, "")

		// Center placeholder text manually.
		centerPad := func(text string) string {
			pad := (m.width - len(text)) / 2
			if pad < 0 {
				pad = 0
			}
			line := fmt.Sprintf("%*s", pad, "") + text
			// Pad to full width.
			if len(line) < m.width {
				line += fmt.Sprintf("%*s", m.width-len(line), "")
			}
			return lipgloss.NewStyle().Foreground(t.TextMuted()).Render(line)
		}

		topPad := max(0, m.height/2-1)
		var lines []string
		for i := 0; i < topPad; i++ {
			lines = append(lines, emptyLine)
		}
		lines = append(lines, centerPad("No message selected"), centerPad("Select a message to preview"))
		for len(lines) < m.height {
			lines = append(lines, emptyLine)
		}
		return strings.Join(lines[:m.height], "\n")
	}

	msg := m.message

	// Header section
	labelStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	valueStyle := lipgloss.NewStyle().Foreground(t.Text())
	subjStyle := lipgloss.NewStyle().Foreground(t.TextEmphasized()).Bold(true)

	var allLines []string
	allLines = append(allLines,
		labelStyle.Render("From: ")+valueStyle.Render(sanitize(msg.From)),
		labelStyle.Render("To:   ")+valueStyle.Render(sanitize(msg.To)),
		labelStyle.Render("Date: ")+valueStyle.Render(msg.Date.Format("Jan 2, 2006 3:04 PM")),
		subjStyle.Render(sanitize(msg.Subject)),
		lipgloss.NewStyle().Foreground(t.BorderDim()).Render(strings.Repeat("─", m.width)),
	)

	// Body with word wrapping (prepared once per message, not per frame).
	wrapped := wrappedBody(msg.Body, msg.HTMLBody, max(10, m.width-1))
	for _, bl := range strings.Split(wrapped, "\n") {
		// Hard-truncate lines that exceed width (wordwrap doesn't break long words).
		runes := []rune(bl)
		if len(runes) > m.width {
			bl = string(runes[:m.width])
		}
		allLines = append(allLines, lipgloss.NewStyle().Foreground(t.Text()).Render(bl))
	}

	// Attachment list
	if len(msg.Attachments) > 0 {
		allLines = append(allLines,
			"",
			lipgloss.NewStyle().Foreground(t.BorderDim()).Render(strings.Repeat("─", m.width)),
			lipgloss.NewStyle().Foreground(t.TextEmphasized()).Bold(true).Render(
				fmt.Sprintf("Attachments (%d)", len(msg.Attachments))),
		)
		for _, att := range msg.Attachments {
			label := fmt.Sprintf("  %s  %s", sanitize(att.Filename), formatSize(att.Size))
			allLines = append(allLines,
				lipgloss.NewStyle().Foreground(t.Text()).Render(label),
			)
		}
		allLines = append(allLines,
			lipgloss.NewStyle().Foreground(t.TextMuted()).Render("  Press 'S' to save attachments"),
		)
	}

	// Hint for browser viewing
	allLines = append(allLines,
		"",
		lipgloss.NewStyle().Foreground(t.TextMuted()).Render("Press 'o' to open in browser · Enter in this pane: Vim reader"),
	)

	// Apply scroll offset
	start := min(m.scrollOffset, len(allLines))
	visible := allLines[start:]

	// Take visible lines up to height, padding each to m.width visible chars.
	emptyLine := fmt.Sprintf("%*s", m.width, "")
	var lines []string
	for i := 0; i < m.height && i < len(visible); i++ {
		line := visible[i]
		// Measure visible width (excluding ANSI codes) and pad with spaces.
		visW := lipgloss.Width(line)
		if visW < m.width {
			line += fmt.Sprintf("%*s", m.width-visW, "")
		}
		lines = append(lines, line)
	}

	// Pad remaining height
	for len(lines) < m.height {
		lines = append(lines, emptyLine)
	}

	return strings.Join(lines[:m.height], "\n")
}

// sanitize strips newlines and control characters that break fixed-row layout.
func sanitize(s string) string {
	return email.SanitizeLine(s)
}

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// sanitizeBody strips ANSI escape sequences, control characters, and
// problematic Unicode from email body text. HTML-stripped emails often contain
// exotic whitespace (NBSP, figure space), zero-width characters (soft hyphen,
// combining marks, joiners) and escape codes that corrupt TUI rendering.
// It also collapses consecutive blank lines to a single blank line.
func sanitizeBody(s string) string {
	// Strip escape sequences and control/bidi characters (also covers mail
	// cached before ingest-time sanitizing existed).
	s = email.SanitizeText(s)
	s = reANSI.ReplaceAllString(s, "")
	// Decode leftover HTML entities (&zwnj;, &nbsp;, etc.) that may remain
	// in cached text bodies from imperfect HTML-to-text conversion.
	s = html.UnescapeString(s)
	var b []rune
	for _, r := range s {
		switch {
		case r == '\n':
			b = append(b, r)
		case r < 0x20:
			// Control chars (\r, \t, ESC, etc.) → skip (newline handled above).
		case r == 0x00AD: // Soft hyphen — zero-width, causes width mismatch.
		case r >= 0x0300 && r <= 0x036F: // Combining diacritical marks.
		case r == 0x034F: // Combining grapheme joiner.
		case r >= 0x200B && r <= 0x200F: // Zero-width space/joiners, LTR/RTL marks.
		case r >= 0x2028 && r <= 0x202F: // Line/paragraph separators, bidi controls.
		case r == 0xFEFF: // BOM / zero-width no-break space.
		case r >= 0xFE00 && r <= 0xFE0F: // Variation selectors.
		case r == 0x00A0, // Non-breaking space → regular space.
			r >= 0x2000 && r <= 0x200A: // En/em/figure/thin/hair spaces → regular space.
			b = append(b, ' ')
		default:
			b = append(b, r)
		}
	}

	// Drop whitespace-only lines and collapse consecutive blank lines to one.
	lines := strings.Split(string(b), "\n")
	var out []string
	prevBlank := true // start true to trim leading blank lines
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if trimmed == "" {
			if !prevBlank {
				out = append(out, "")
			}
			prevBlank = true
		} else {
			out = append(out, trimmed)
			prevBlank = false
		}
	}
	// Trim trailing blank line.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// maxPreviewBody caps how much of a body the preview renders. Bodies are
// attacker-controlled and can be tens of megabytes.
const maxPreviewBody = 512 * 1024

// bodyMemo caches the prepared body of the message on screen. View runs every
// frame, so sanitizing, JSON detection and wrapping must not be repeated there:
// a crafted body would otherwise freeze the UI on every render.
var bodyMemo struct {
	sync.Mutex
	body, html string
	width      int
	wrapped    string
	valid      bool
}

// wrappedBody returns the display-ready, word-wrapped body text.
func wrappedBody(body, htmlBody string, width int) string {
	bodyMemo.Lock()
	defer bodyMemo.Unlock()
	if bodyMemo.valid && bodyMemo.width == width && bodyMemo.body == body && bodyMemo.html == htmlBody {
		return bodyMemo.wrapped
	}
	text := preparedBody(body, htmlBody)
	bodyMemo.body, bodyMemo.html, bodyMemo.width = body, htmlBody, width
	bodyMemo.wrapped, bodyMemo.valid = wordwrap.String(breakLongWords(text, width), width), true
	return bodyMemo.wrapped
}

// preparedBody returns the sanitized, display-ready body text before wrapping:
// plain text (JSON pretty-printed) or stripped HTML, truncated for very large
// messages.
func preparedBody(body, htmlBody string) string {
	text := body
	truncated := false
	if len(text) > maxPreviewBody {
		text, truncated = strings.ToValidUTF8(text[:maxPreviewBody], ""), true
	}
	text = prettyJSON(sanitizeBody(text))
	if text == "" && htmlBody != "" {
		// HTML-only email — do a basic tag strip for preview.
		h := htmlBody
		if len(h) > maxPreviewBody {
			h, truncated = strings.ToValidUTF8(h[:maxPreviewBody], ""), true
		}
		text = sanitizeBody(basicHTMLStrip(h))
		if text == "" {
			text = "(HTML-only email — press 'o' to view)"
		}
	}
	if text == "" {
		text = "(loading...)"
	}
	if truncated {
		text += "\n\n[message truncated in preview — press 'o' to open the full message]"
	}
	return text
}

// breakLongWords hard-breaks any unbroken run longer than width. The wordwrap
// library is quadratic in the length of a single word, so one long base64
// blob or URL (trivial for a sender to include) would stall rendering.
func breakLongWords(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/width + 1)
	run := 0
	for _, r := range s {
		if r == '\n' || r == ' ' || r == '\t' {
			run = 0
		} else {
			if run == width {
				b.WriteByte('\n')
				run = 0
			}
			run++
		}
		b.WriteRune(r)
	}
	return b.String()
}

// prettyJSON budgets: each failed decode attempt can scan a long way, so both
// the input size and the number of attempts are bounded.
const (
	maxPrettyJSONInput    = 256 * 1024
	maxPrettyJSONAttempts = 32
)

// prettyJSON finds JSON objects/arrays in text and pretty-prints them in place.
func prettyJSON(s string) string {
	if len(s) > maxPrettyJSONInput || !strings.ContainsAny(s, "{[") {
		return s
	}
	var result strings.Builder
	i := 0
	attempts := 0
	data := []byte(s)
	for i < len(data) {
		if (data[i] == '{' || data[i] == '[') && attempts < maxPrettyJSONAttempts {
			attempts++
			dec := json.NewDecoder(strings.NewReader(s[i:]))
			var raw json.RawMessage
			if err := dec.Decode(&raw); err == nil && len(raw) > 10 {
				pretty, err := json.MarshalIndent(raw, "", "  ")
				if err == nil {
					result.Write(pretty)
					i += int(dec.InputOffset())
					continue
				}
			}
		}
		result.WriteByte(data[i])
		i++
	}
	return result.String()
}

// basicHTMLStrip is a simple regex-based HTML tag remover for preview fallback.
// Used when the text body is empty but HTML is available.
func basicHTMLStrip(h string) string {
	// Remove style and script blocks.
	reBlock := regexp.MustCompile(`(?is)<(style|script)[^>]*>.*?</\1>`)
	h = reBlock.ReplaceAllString(h, "")
	// Replace <br> and block-level tags with newlines.
	reBR := regexp.MustCompile(`(?i)<br\s*/?>`)
	h = reBR.ReplaceAllString(h, "\n")
	reBlock2 := regexp.MustCompile(`(?i)</?(p|div|tr|li|h[1-6])[^>]*>`)
	h = reBlock2.ReplaceAllString(h, "\n")
	// Strip remaining tags.
	reTags := regexp.MustCompile(`<[^>]*>`)
	h = reTags.ReplaceAllString(h, "")
	// Decode entities.
	h = html.UnescapeString(h)
	return h
}

func formatSize(b int) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	case b > 0:
		return fmt.Sprintf("%d B", b)
	default:
		return ""
	}
}

func (m Model) contentHeight() int {
	if m.message == nil {
		return 0
	}
	wrapped := wrappedBody(m.message.Body, m.message.HTMLBody, max(10, m.width-1))
	h := 5 + strings.Count(wrapped, "\n") + 1
	if len(m.message.Attachments) > 0 {
		h += 3 + len(m.message.Attachments) // separator + header + files + save hint
	}
	h += 2 // "open in browser" hint
	return h
}

func (m Model) maxScroll() int {
	return max(0, m.contentHeight()-m.height)
}

func (m Model) ClearMessage() Model {
	m.message = nil
	m.scrollOffset = 0
	return m
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
	if ms := m.maxScroll(); m.scrollOffset > ms {
		m.scrollOffset = ms
	}
	return m
}
