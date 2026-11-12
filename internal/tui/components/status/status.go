package status

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/util"
)

type Model struct {
	width       int
	mode        keys.Mode
	account     string
	folder      string
	syncStatus  string
	infoText    string
	infoIsError bool
	infoExpires time.Time
	motion      bool
	tickArmed   bool
	connected   bool

	// Running background processes (insertion-ordered).
	processes map[string]string
	order     []string
	started   map[string]time.Time
}

func New() Model {
	return Model{
		mode:       keys.ModeNormal,
		account:    "Personal",
		folder:     "Inbox",
		syncStatus: "just now",
		processes:  make(map[string]string),
		started:    make(map[string]time.Time),
		motion:     true,
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
		// Info text may embed server or message-derived strings.
		m.infoText = email.SanitizeLine(msg.Text)
		m.infoIsError = msg.IsError
		ttl := 2 * time.Second
		if msg.IsError {
			ttl = 6 * time.Second
			if !strings.HasPrefix(m.infoText, "ERR:") {
				m.infoText = "ERR: " + m.infoText
			}
		} else if strings.HasPrefix(strings.ToUpper(m.infoText), "WARN:") {
			ttl = 4 * time.Second
		}
		m.infoExpires = time.Now().Add(ttl)
		cmd := m.armTick()
		return m, cmd
	case util.MotionTickMsg:
		m.tickArmed = false
		if !m.infoExpires.IsZero() && !msg.Now.Before(m.infoExpires) {
			m.infoText = ""
			m.infoExpires = time.Time{}
		}
		cmd := m.armTick()
		return m, cmd
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
		if m.started[msg.ID].IsZero() {
			m.started[msg.ID] = time.Now()
		}
		cmd := m.armTick()
		return m, cmd
	case util.ProcessEndMsg:
		delete(m.processes, msg.ID)
		delete(m.started, msg.ID)
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

func (m *Model) armTick() tea.Cmd {
	if m.tickArmed {
		return nil
	}
	interval := time.Duration(0)
	if len(m.processes) > 0 && m.motion {
		interval = 100 * time.Millisecond
	} else if !m.infoExpires.IsZero() {
		interval = time.Until(m.infoExpires)
		if m.motion && interval > 500*time.Millisecond {
			interval = 500 * time.Millisecond
		}
	}
	if interval <= 0 {
		return nil
	}
	m.tickArmed = true
	return tea.Tick(interval, func(now time.Time) tea.Msg { return util.MotionTickMsg{Now: now} })
}

func (m Model) IsFullyIdle() bool { return len(m.processes) == 0 && m.infoExpires.IsZero() }
func (m Model) isFullyIdle() bool { return m.IsFullyIdle() }

func (m Model) TickInterval(now time.Time) time.Duration {
	if len(m.processes) > 0 && m.motion {
		return 100 * time.Millisecond
	}
	if !m.infoExpires.IsZero() {
		d := m.infoExpires.Sub(now)
		if m.motion && d > 500*time.Millisecond {
			return 500 * time.Millisecond
		}
		if d > 0 {
			return d
		}
	}
	return 0
}

func (m Model) SetMotion(enabled bool) Model { m.motion = enabled; return m }

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

func SpinnerFrame(now time.Time) string {
	return string(spinnerFrames[(now.UnixMilli()/100)%int64(len(spinnerFrames))])
}

func ProgressBar(done, total, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	half := done * width * 2 / total
	if half > width*2 {
		half = width * 2
	}
	full := half / 2
	out := strings.Repeat("━", full)
	if half%2 == 1 {
		out += "╸"
	}
	filled := lipgloss.NewStyle().Foreground(theme.Current().Primary()).Render(out)
	dim := lipgloss.NewStyle().Foreground(theme.Current().TextMuted()).Render(strings.Repeat("━", max(0, width-full-(half%2))))
	return filled + dim
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
				if started := m.started[id]; !started.IsZero() && time.Since(started) >= 3*time.Second {
					label += fmt.Sprintf(" (%ds)", int(time.Since(started).Seconds()))
				}
				allLabels = append(allLabels, label)
			}
		}
		// Calculate available width for the process area.
		leftWidth := lipgloss.Width(badge + location + info)
		helpWidth := lipgloss.Width("?:help") + 4 // 2 padding + 2 gap
		availWidth := m.width - leftWidth - helpWidth

		// Fit as many labels as possible, then show "+N more".
		processText := fitLabels(allLabels, availWidth)
		if m.motion {
			processText = SpinnerFrame(time.Now()) + " " + processText
		} else {
			processText = "◌ " + processText
		}
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
