package preview

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/muesli/reflow/wordwrap"
)

type Model struct {
	width        int
	height       int
	focused      bool
	message      *email.Message
	scrollOffset int
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
	case util.FetchBodyCompleteMsg:
		if m.message != nil && m.message.UID == msg.UID {
			m.message.Body = msg.Body
			m.message.HTMLBody = msg.HTMLBody
			m.scrollOffset = 0
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
		return m, m.openInBrowser()
	}
	return m, nil
}

// openInBrowser writes the HTML body to a temp file and opens it.
func (m Model) openInBrowser() tea.Cmd {
	if m.message == nil || m.message.HTMLBody == "" {
		return func() tea.Msg {
			return util.InfoMsg{Text: "No HTML content to open", IsError: false}
		}
	}
	return func() tea.Msg {
		dir := filepath.Join(os.TempDir(), "vimail")
		os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, fmt.Sprintf("msg-%d.html", m.message.UID))
		if err := os.WriteFile(path, []byte(m.message.HTMLBody), 0600); err != nil {
			return util.InfoMsg{Text: "Failed to write HTML: " + err.Error(), IsError: true}
		}
		if err := exec.Command("open", path).Start(); err != nil {
			return util.InfoMsg{Text: "Failed to open browser: " + err.Error(), IsError: true}
		}
		return util.InfoMsg{Text: "Opened in browser", IsError: false}
	}
}

func (m Model) View() string {
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

	// Body with word wrapping
	bodyText := sanitizeBody(msg.Body)
	if bodyText == "" {
		bodyText = "(loading...)"
	}
	bodyWidth := max(10, m.width-1)
	wrapped := wordwrap.String(bodyText, bodyWidth)
	for _, bl := range strings.Split(wrapped, "\n") {
		// Hard-truncate lines that exceed width (wordwrap doesn't break long words).
		runes := []rune(bl)
		if len(runes) > m.width {
			bl = string(runes[:m.width])
		}
		allLines = append(allLines, lipgloss.NewStyle().Foreground(t.Text()).Render(bl))
	}

	// Hint for HTML viewing
	if msg.HTMLBody != "" {
		allLines = append(allLines,
			"",
			lipgloss.NewStyle().Foreground(t.TextMuted()).Render("Press 'o' to open HTML in browser"),
		)
	}

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
	var b []rune
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b = append(b, ' ')
		} else if r >= 0x20 {
			b = append(b, r)
		}
	}
	return string(b)
}

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// sanitizeBody strips ANSI escape sequences, control characters, and
// problematic Unicode from email body text. HTML-stripped emails often contain
// exotic whitespace (NBSP, figure space), zero-width characters (soft hyphen,
// combining marks, joiners) and escape codes that corrupt TUI rendering.
// It also collapses consecutive blank lines to a single blank line.
func sanitizeBody(s string) string {
	// Strip any ANSI escape sequences.
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

func (m Model) contentHeight() int {
	if m.message == nil {
		return 0
	}
	bodyWidth := max(10, m.width-1)
	wrapped := wordwrap.String(m.message.Body, bodyWidth)
	extra := 0
	if m.message.HTMLBody != "" {
		extra = 2
	}
	return 5 + strings.Count(wrapped, "\n") + 1 + extra
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
