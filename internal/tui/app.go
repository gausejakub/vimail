package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gause/vmail/internal/config"
	"github.com/gause/vmail/internal/mock"
	"github.com/gause/vmail/internal/theme"
	"github.com/gause/vmail/internal/tui/components/compose"
	"github.com/gause/vmail/internal/tui/components/help"
	"github.com/gause/vmail/internal/tui/components/mailbox"
	"github.com/gause/vmail/internal/tui/components/msglist"
	"github.com/gause/vmail/internal/tui/components/preview"
	"github.com/gause/vmail/internal/tui/components/status"
	"github.com/gause/vmail/internal/tui/keys"
	"github.com/gause/vmail/internal/tui/layout"
	"github.com/gause/vmail/internal/tui/util"
)

type Model struct {
	cfg    config.Config
	width  int
	height int

	mode        keys.Mode
	focusedPane layout.Pane
	showHelp    bool
	layout      layout.SplitPaneLayout

	mailbox mailbox.Model
	msglist msglist.Model
	preview preview.Model
	status  status.Model
	compose compose.Model

	cmdInput  textinput.Model
	cmdActive bool
}

func New(cfg config.Config) Model {
	theme.SetCurrent(cfg.Theme.Name)

	cmdInput := textinput.New()
	cmdInput.Prompt = ":"
	cmdInput.Placeholder = ""

	m := Model{
		cfg:         cfg,
		mode:        keys.ModeNormal,
		focusedPane: layout.PaneMsgList,
		layout:      layout.SplitPaneLayout{ShowPreview: cfg.General.PreviewPane},
		mailbox:     mailbox.New(),
		msglist:     msglist.New().Focus(),
		preview:     preview.New(),
		status:      status.New(),
		compose:     compose.New(),
		cmdInput:    cmdInput,
	}
	return m
}

func (m Model) Init() tea.Cmd {
	// Auto-select first message so preview isn't empty on launch
	msgs := mock.MessagesFor(mock.Accounts[0].Email, "Inbox")
	if len(msgs) > 0 {
		return func() tea.Msg {
			return util.MessageSelectedMsg{Message: msgs[0]}
		}
	}
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout.Resize(msg.Width, msg.Height, 1)
		m = m.resizePanes()
		m.status = m.status.SetWidth(msg.Width)

	case util.FolderSelectedMsg:
		var cmd tea.Cmd
		m.msglist, cmd = m.msglist.Update(msg)
		cmds = append(cmds, cmd)
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		// Auto-select first message in new folder
		msgs := mock.MessagesFor(msg.Account, msg.Folder)
		if len(msgs) > 0 {
			cmds = append(cmds, func() tea.Msg {
				return util.MessageSelectedMsg{Message: msgs[0]}
			})
		} else {
			m.preview = m.preview.ClearMessage()
		}
		return m, tea.Batch(cmds...)

	case util.MessageSelectedMsg:
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case util.ComposeCloseMsg:
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		return m, tea.Batch(cmds...)

	case util.ComposeSaveDraftMsg:
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		currentEmail := m.mailbox.SelectedEmail()
		draftID := msg.DraftID
		if draftID == "" {
			draftID = mock.NextDraftID()
		}
		mock.SaveDraft(currentEmail, mock.Message{
			ID:      draftID,
			From:    currentEmail,
			To:      msg.To,
			Subject: msg.Subject,
			Body:    msg.Body,
			Date:    time.Now(),
		})
		// Refresh msglist if currently viewing Drafts
		if m.msglist.CurrentFolder() == "Drafts" {
			cmds = append(cmds, func() tea.Msg {
				return util.FolderSelectedMsg{Account: currentEmail, Folder: "Drafts"}
			})
		}
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		cmds = append(cmds, func() tea.Msg {
			return util.InfoMsg{Text: "Draft saved", IsError: false}
		})
		return m, tea.Batch(cmds...)

	case util.OpenDraftMsg:
		draft := msg.Message
		m.compose = m.compose.ShowDraft(draft.ID, draft.To, draft.Subject, draft.Body).SetSize(m.width, m.height)
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		return m, tea.Batch(cmds...)

	case util.ComposeSubmitMsg:
		draftID := m.compose.DraftID()
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		currentEmail := m.mailbox.SelectedEmail()
		// Remove draft if we were editing one
		if draftID != "" {
			mock.DeleteDraft(currentEmail, draftID)
		}
		m.mailbox = m.mailbox.SelectFolder(currentEmail, "Sent")
		cmds = append(cmds, func() tea.Msg {
			return util.FolderSelectedMsg{Account: currentEmail, Folder: "Sent"}
		})
		cmds = append(cmds, func() tea.Msg {
			return util.InfoMsg{Text: "Message sent (mock)", IsError: false}
		})
		return m, tea.Batch(cmds...)

	case util.InfoMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case keys.ModeChangedMsg:
		m.mode = msg.Mode
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Compose overlay eats all keys when visible
		if m.compose.Visible() {
			var cmd tea.Cmd
			m.compose, cmd = m.compose.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Help overlay
		if m.showHelp {
			if msg.String() == "?" || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}

		// Command mode
		if m.cmdActive {
			return m.handleCommandKey(msg)
		}

		// Normal/Visual mode
		return m.handleNormalKey(msg)
	}

	// Forward non-key messages to compose when visible (e.g. cursor blink)
	if m.compose.Visible() {
		var cmd tea.Cmd
		m.compose, cmd = m.compose.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Pass through other messages to status bar
	var cmd tea.Cmd
	m.status, cmd = m.status.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keys.Normal.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Normal.Help):
		m.showHelp = true

	case key.Matches(msg, keys.Normal.Command):
		m.cmdActive = true
		m.cmdInput.SetValue("")
		m.cmdInput.Focus()
		m.mode = keys.ModeCommand
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeCommand}
		})

	case key.Matches(msg, keys.Normal.Compose):
		m.compose = m.compose.Show().SetSize(m.width, m.height)
		m.mode = keys.ModeInsert
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeInsert}
		})
		cmds = append(cmds, textinput.Blink)

	case key.Matches(msg, keys.Normal.Reply):
		if selected := m.msglist.SelectedMessage(); selected != nil {
			// Extract email from "Name <email>" format
			replyTo := selected.From
			if idx := strings.Index(replyTo, "<"); idx >= 0 {
				end := strings.Index(replyTo, ">")
				if end > idx {
					replyTo = replyTo[idx+1 : end]
				}
			}
			// Build reply subject
			replySubj := selected.Subject
			if !strings.HasPrefix(strings.ToLower(replySubj), "re:") {
				replySubj = "Re: " + replySubj
			}
			// Quoted body as editor content
			quoted := "\n> " + strings.ReplaceAll(selected.Body, "\n", "\n> ")
			m.compose = m.compose.ShowReply(replyTo, replySubj, quoted).SetSize(m.width, m.height)
			m.mode = keys.ModeNormal
			cmds = append(cmds, func() tea.Msg {
				return keys.ModeChangedMsg{Mode: keys.ModeNormal}
			})
		}

	case key.Matches(msg, keys.Normal.Enter):
		if m.focusedPane == layout.PaneMsgList {
			if selected := m.msglist.SelectedMessage(); selected != nil {
				if m.msglist.CurrentFolder() == "Drafts" {
					draft := *selected
					cmds = append(cmds, func() tea.Msg {
						return util.OpenDraftMsg{Message: draft}
					})
					return m, tea.Batch(cmds...)
				}
			}
		}

	case key.Matches(msg, keys.Normal.NextPane):
		m = m.cycleFocus(1)

	case key.Matches(msg, keys.Normal.PrevPane):
		m = m.cycleFocus(-1)

	default:
		// h/l at app level for pane switching
		k := msg.String()
		if k == "l" || k == "right" {
			m = m.cycleFocus(1)
		} else if k == "h" || k == "left" {
			m = m.cycleFocus(-1)
		} else {
			// Forward to focused pane
			var cmd tea.Cmd
			switch m.focusedPane {
			case layout.PaneMailbox:
				m.mailbox, cmd = m.mailbox.HandleKey(k)
			case layout.PaneMsgList:
				m.msglist, cmd = m.msglist.HandleKey(k)
			case layout.PanePreview:
				m.preview, cmd = m.preview.HandleKey(k)
			}
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keys.Command.Cancel):
		m.cmdActive = false
		m.cmdInput.Blur()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})

	case key.Matches(msg, keys.Command.Submit):
		cmd := m.executeCommand(m.cmdInput.Value())
		m.cmdActive = false
		m.cmdInput.Blur()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	default:
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) executeCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "quit", "q":
		return tea.Quit

	case "theme":
		if len(parts) < 2 {
			return func() tea.Msg {
				return util.InfoMsg{Text: "Usage: :theme <name>", IsError: true}
			}
		}
		name := parts[1]
		theme.SetCurrent(name)
		return func() tea.Msg {
			return util.InfoMsg{Text: "Theme: " + name, IsError: false}
		}

	case "sync":
		return func() tea.Msg {
			return util.InfoMsg{Text: "Sync not implemented yet", IsError: false}
		}

	default:
		return func() tea.Msg {
			return util.InfoMsg{Text: "Unknown command: " + parts[0], IsError: true}
		}
	}
}

func (m Model) cycleFocus(dir int) Model {
	m = m.blurAll()

	maxPane := 2
	if !m.layout.ShowPreview {
		maxPane = 1
	}

	next := (int(m.focusedPane) + dir + maxPane + 1) % (maxPane + 1)
	m.focusedPane = layout.Pane(next)

	switch m.focusedPane {
	case layout.PaneMailbox:
		m.mailbox = m.mailbox.Focus()
	case layout.PaneMsgList:
		m.msglist = m.msglist.Focus()
	case layout.PanePreview:
		m.preview = m.preview.Focus()
	}
	return m
}

func (m Model) blurAll() Model {
	m.mailbox = m.mailbox.Blur()
	m.msglist = m.msglist.Blur()
	m.preview = m.preview.Blur()
	return m
}

func (m Model) resizePanes() Model {
	ml := m.layout
	// Components get inner dimensions (minus 2 for border on each axis)
	m.mailbox = m.mailbox.SetSize(max(0, ml.MailboxWidth-2), max(0, ml.PaneHeight-2))
	m.msglist = m.msglist.SetSize(max(0, ml.MsgListWidth-2), max(0, ml.PaneHeight-2))
	if ml.ShowPreview {
		m.preview = m.preview.SetSize(max(0, ml.PreviewWidth-2), max(0, ml.PaneHeight-2))
	}
	m.compose = m.compose.SetSize(m.width, m.height)
	return m
}

func (m Model) View() string {
	// Render panes inside containers
	mailboxView := layout.Container{
		Width:   m.layout.MailboxWidth,
		Height:  m.layout.PaneHeight,
		Focused: m.focusedPane == layout.PaneMailbox,
		Border:  layout.BorderRounded,
	}.Render(m.mailbox.View())

	msglistView := layout.Container{
		Width:   m.layout.MsgListWidth,
		Height:  m.layout.PaneHeight,
		Focused: m.focusedPane == layout.PaneMsgList,
		Border:  layout.BorderRounded,
	}.Render(m.msglist.View())

	var previewView string
	if m.layout.ShowPreview {
		previewView = layout.Container{
			Width:   m.layout.PreviewWidth,
			Height:  m.layout.PaneHeight,
			Focused: m.focusedPane == layout.PanePreview,
			Border:  layout.BorderRounded,
		}.Render(m.preview.View())
	}

	panes := m.layout.Compose(mailboxView, msglistView, previewView)

	// Status bar or command input
	var bottomBar string
	if m.cmdActive {
		bottomBar = m.cmdInput.View()
	} else {
		bottomBar = m.status.View()
	}

	screen := panes + "\n" + bottomBar

	// Overlays
	if m.showHelp {
		helpView := help.View(m.width)
		screen = layout.PlaceOverlayCentered(helpView, screen, m.width, m.height)
	}

	if m.compose.Visible() {
		composeView := m.compose.View()
		screen = layout.PlaceOverlayCentered(composeView, screen, m.width, m.height)
	}

	return screen
}
