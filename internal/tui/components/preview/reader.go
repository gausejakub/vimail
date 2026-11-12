package preview

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/pkg/vimtea"
)

// Reading reports whether the Vim reader is open; it then takes every key.
func (m Model) Reading() bool {
	return m.reading
}

// StartReading opens the current message in a read-only Vim buffer: headers,
// body (unwrapped, so copied paragraphs keep their original lines) and the
// attachment list. Yanks are emitted as vimtea.YankMsg for the clipboard.
func (m Model) StartReading() (Model, tea.Cmd) {
	if m.message == nil {
		return m, nil
	}
	m.reader = vimtea.NewEditor(
		vimtea.WithContent(readerContent(m)),
		vimtea.WithReadOnly(true),
		vimtea.WithWrap(true),
		vimtea.WithEnableStatusBar(false),
		vimtea.WithEnableModeCommand(false),
		vimtea.WithRelativeNumbers(false),
	)
	m.reading = true
	m = m.sizeReader()
	return m, m.reader.Init()
}

// StopReading closes the Vim reader.
func (m Model) StopReading() Model {
	m.reading = false
	m.reader = nil
	return m
}

// ReaderUpdate forwards a message to the reader. q or Esc in normal mode
// closes it; Esc in visual mode only ends the selection, as in Vim.
func (m Model) ReaderUpdate(msg tea.Msg) (Model, tea.Cmd) {
	if !m.reading || m.reader == nil {
		return m, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok && m.reader.GetMode() == vimtea.ModeNormal {
		switch k.String() {
		case "q", "esc":
			return m.StopReading(), nil
		}
	}
	updated, cmd := m.reader.Update(msg)
	m.reader = updated.(vimtea.Editor)
	return m, cmd
}

func (m Model) sizeReader() Model {
	if m.reader != nil {
		sized, _ := m.reader.SetSize(m.width, max(1, m.height-1))
		m.reader = sized.(vimtea.Editor)
	}
	return m
}

func (m Model) readerView() string {
	t := theme.Current()
	m = m.sizeReader()
	// Keep exactly height-1 editor rows so the hint line is never pushed out
	// of the pane, whatever the editor's own row accounting.
	rows := strings.Split(m.reader.View(), "\n")
	bodyRows := max(1, m.height-1)
	if len(rows) > bodyRows {
		rows = rows[:bodyRows]
	}
	for len(rows) < bodyRows {
		rows = append(rows, strings.Repeat(" ", m.width))
	}
	body := strings.Join(rows, "\n")

	mode := m.reader.GetMode().String()
	hint := readerHint(m.reader.GetStatusText(), mode)
	status := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Primary()).Bold(true).Render(" " + mode + " ")
	rest := lipgloss.NewStyle().Foreground(t.TextMuted()).Render(" " + hint)
	line := status + rest
	if w := lipgloss.Width(line); w < m.width {
		line += strings.Repeat(" ", m.width-w)
	}
	return body + "\n" + line
}

// readerContent is the plain text shown in the reader.
func readerContent(m Model) string {
	msg := m.message
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\nTo:   %s\nDate: %s\nSubject: %s\n\n",
		sanitize(msg.From), sanitize(msg.To), msg.Date.Format("Jan 2, 2006 3:04 PM"), sanitize(msg.Subject))
	b.WriteString(preparedBody(msg.Body, msg.HTMLBody))
	if len(msg.Attachments) > 0 {
		fmt.Fprintf(&b, "\n\nAttachments (%d)\n", len(msg.Attachments))
		for _, att := range msg.Attachments {
			fmt.Fprintf(&b, "  %s  %s\n", sanitize(att.Filename), formatSize(att.Size))
		}
	}
	return b.String()
}

// readerHint shows the editor's transient status (search prompt, pending keys,
// messages) without repeating the mode, or the key help when there is none.
func readerHint(status, mode string) string {
	s := strings.TrimSpace(status)
	s = strings.TrimSpace(strings.TrimPrefix(s, mode))
	s = strings.TrimSpace(strings.TrimPrefix(s, "|"))
	if s == "" {
		return "v/V select · y copy · / search · q back"
	}
	return s
}
