package status

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/util"
)

type clearInfoMsg struct{}

type Model struct {
	width       int
	mode        keys.Mode
	account     string
	folder      string
	syncStatus  string
	infoText    string
	infoIsError bool
	connected   bool

	// Running background processes (insertion-ordered).
	processes map[string]string
	order     []string
}

func New() Model {
	return Model{
		mode:       keys.ModeNormal,
		account:    "Personal",
		folder:     "Inbox",
		syncStatus: "just now",
		processes:  make(map[string]string),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case keys.ModeChangedMsg:
		m.mode = msg.Mode
	case util.InfoMsg:
		m.infoText = msg.Text
		m.infoIsError = msg.IsError
		return m, tea.Tick(4*time.Second, func(time.Time) tea.Msg {
			return clearInfoMsg{}
		})
	case clearInfoMsg:
		m.infoText = ""
	case util.SyncStatusMsg:
		m.syncStatus = msg.LastSyncAgo
	case util.FolderSelectedMsg:
		m.account = msg.Account
		m.folder = msg.Folder
	case util.ProcessStartMsg:
		if _, exists := m.processes[msg.ID]; !exists {
			m.order = append(m.order, msg.ID)
		}
		m.processes[msg.ID] = msg.Label
	case util.ProcessEndMsg:
		delete(m.processes, msg.ID)
		for i, id := range m.order {
			if id == msg.ID {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	case util.SyncAllCompleteMsg:
		m.syncStatus = "just now"
		m.connected = true
	case util.ConnectionStatusMsg:
		m.connected = msg.Connected
	}
	return m, nil
}

func (m Model) View() string {
	t := theme.Current()

	modeColor := t.NormalMode()
	switch m.mode {
	case keys.ModeInsert:
		modeColor = t.InsertMode()
	case keys.ModeVisual:
		modeColor = t.VisualMode()
	case keys.ModeCommand:
		modeColor = t.CommandMode()
	}

	badge := lipgloss.NewStyle().
		Background(modeColor).
		Foreground(t.Background()).
		Bold(true).
		Padding(0, 1).
		Render(m.mode.String())

	bg := t.BackgroundDarker()

	location := lipgloss.NewStyle().
		Foreground(t.Text()).
		Background(bg).
		Padding(0, 1).
		Render(m.account + " > " + m.folder)

	info := ""
	if m.infoText != "" {
		fg := t.Info()
		if m.infoIsError {
			fg = t.Error()
		}
		info = lipgloss.NewStyle().
			Foreground(fg).
			Background(bg).
			Padding(0, 1).
			Render(m.infoText)
	}

	var sync string
	if len(m.processes) > 0 {
		// Collect all labels in order.
		var allLabels []string
		for _, id := range m.order {
			if label, ok := m.processes[id]; ok {
				allLabels = append(allLabels, label)
			}
		}
		// Calculate available width for the process area.
		leftWidth := lipgloss.Width(badge + location + info)
		helpWidth := lipgloss.Width("?:help") + 4 // 2 padding + 2 gap
		availWidth := m.width - leftWidth - helpWidth

		// Fit as many labels as possible, then show "+N more".
		processText := fitLabels(allLabels, availWidth)
		sync = lipgloss.NewStyle().
			Foreground(t.Info()).
			Background(bg).
			Render(processText)
	} else {
		var syncText string
		syncFg := t.TextMuted()
		if m.connected {
			syncText = "● ↻ " + m.syncStatus
			syncFg = t.Success()
		} else {
			syncText = "↻ " + m.syncStatus
		}
		sync = lipgloss.NewStyle().
			Foreground(syncFg).
			Background(bg).
			Render(syncText)
	}

	help := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Background(bg).
		Render("?:help")

	left := badge + location + info
	right := sync + "  " + help

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	gap := max(0, m.width-leftWidth-rightWidth)
	filler := lipgloss.NewStyle().
		Background(t.BackgroundDarker()).
		Width(gap).
		Render("")

	return lipgloss.NewStyle().
		Background(t.BackgroundDarker()).
		Width(m.width).
		MaxWidth(m.width).
		Render(left + filler + right)
}

func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

// Processes returns the ordered list of active process labels.
func (m Model) Processes() []string {
	var labels []string
	for _, id := range m.order {
		if label, ok := m.processes[id]; ok {
			labels = append(labels, label)
		}
	}
	return labels
}

// ProcessCount returns the number of active processes.
func (m Model) ProcessCount() int {
	return len(m.processes)
}

// fitLabels joins as many labels as fit within maxWidth, appending "+N more" if truncated.
func fitLabels(labels []string, maxWidth int) string {
	if len(labels) == 0 {
		return ""
	}
	sep := " │ "
	// Try all labels first.
	full := strings.Join(labels, sep)
	if lipgloss.Width(full) <= maxWidth {
		return full
	}
	// Fit progressively fewer labels.
	for i := len(labels) - 1; i >= 1; i-- {
		remaining := len(labels) - i
		suffix := fmt.Sprintf(" +%d more", remaining)
		partial := strings.Join(labels[:i], sep) + suffix
		if lipgloss.Width(partial) <= maxWidth {
			return partial
		}
	}
	// Just show count if even one label doesn't fit.
	return fmt.Sprintf("⟳ %d processes", len(labels))
}
