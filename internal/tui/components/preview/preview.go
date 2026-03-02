package preview

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gause/vmail/internal/mock"
	"github.com/gause/vmail/internal/theme"
	"github.com/gause/vmail/internal/tui/util"
	"github.com/muesli/reflow/wordwrap"
)

type Model struct {
	width        int
	height       int
	focused      bool
	message      *mock.Message
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
	}
	return m, nil
}

func (m Model) View() string {
	t := theme.Current()

	if m.message == nil {
		placeholder := lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Width(m.width).
			Align(lipgloss.Center).
			Render("No message selected")

		hint := lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Width(m.width).
			Align(lipgloss.Center).
			Render("Select a message to preview")

		topPad := max(0, m.height/2-1)
		var lines []string
		for i := 0; i < topPad; i++ {
			lines = append(lines, lipgloss.NewStyle().Width(m.width).Render(""))
		}
		lines = append(lines, placeholder, hint)
		for len(lines) < m.height {
			lines = append(lines, lipgloss.NewStyle().Width(m.width).Render(""))
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
		labelStyle.Render("From: ")+valueStyle.Render(msg.From),
		labelStyle.Render("To:   ")+valueStyle.Render(msg.To),
		labelStyle.Render("Date: ")+valueStyle.Render(msg.Date.Format("Jan 2, 2006 3:04 PM")),
		subjStyle.Render(msg.Subject),
		lipgloss.NewStyle().Foreground(t.BorderDim()).Width(m.width).Render(strings.Repeat("─", m.width)),
	)

	// Body with word wrapping
	bodyWidth := max(10, m.width-1)
	wrapped := wordwrap.String(msg.Body, bodyWidth)
	for _, bl := range strings.Split(wrapped, "\n") {
		allLines = append(allLines, lipgloss.NewStyle().Foreground(t.Text()).Render(bl))
	}

	// Apply scroll offset
	start := min(m.scrollOffset, len(allLines))
	visible := allLines[start:]

	// Take visible lines up to height
	var lines []string
	for i := 0; i < m.height && i < len(visible); i++ {
		lines = append(lines, fmt.Sprintf("%-*s", m.width, visible[i]))
	}

	// Pad remaining height
	for len(lines) < m.height {
		lines = append(lines, fmt.Sprintf("%-*s", m.width, ""))
	}

	return strings.Join(lines[:m.height], "\n")
}

func (m Model) contentHeight() int {
	if m.message == nil {
		return 0
	}
	bodyWidth := max(10, m.width-1)
	wrapped := wordwrap.String(m.message.Body, bodyWidth)
	return 5 + strings.Count(wrapped, "\n") + 1
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
