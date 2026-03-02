package status

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gause/vmail/internal/theme"
	"github.com/gause/vmail/internal/tui/keys"
	"github.com/gause/vmail/internal/tui/util"
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
}

func New() Model {
	return Model{
		mode:       keys.ModeNormal,
		account:    "Personal",
		folder:     "Inbox",
		syncStatus: "just now",
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

	sync := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Background(bg).
		Render("↻ " + m.syncStatus)

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
